/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ipdenylist

import (
	"fmt"
	"sort"
	"strings"

	networking "k8s.io/api/networking/v1"
	"k8s.io/ingress-nginx/internal/net"

	"k8s.io/ingress-nginx/internal/ingress/annotations/parser"
	ing_errors "k8s.io/ingress-nginx/internal/ingress/errors"
	"k8s.io/ingress-nginx/internal/ingress/resolver"
	"k8s.io/ingress-nginx/pkg/util/sets"
)

const (
	ipDenylistAnnotation = "denylist-source-range"
)

var denylistAnnotations = parser.Annotation{
	Group: "acl",
	Annotations: parser.AnnotationFields{
		ipDenylistAnnotation: {
			Validator:     parser.ValidateCIDRs,
			Scope:         parser.AnnotationScopeLocation,
			Risk:          parser.AnnotationRiskMedium, // Failure on parsing this may cause undesired access
			Documentation: `This annotation allows setting a list of IPs and networks that should be blocked to access this Location`,
		},
	},
}

// SourceRange returns the CIDR
type SourceRange struct {
	CIDR []string `json:"cidr,omitempty"`
}

// Equal tests for equality between two SourceRange types
func (sr1 *SourceRange) Equal(sr2 *SourceRange) bool {
	if sr1 == sr2 {
		return true
	}
	if sr1 == nil || sr2 == nil {
		return false
	}

	return sets.StringElementsMatch(sr1.CIDR, sr2.CIDR)
}

type ipdenylist struct {
	r                resolver.Resolver
	annotationConfig parser.Annotation
}

// NewParser creates a new denylist annotation parser
func NewParser(r resolver.Resolver) parser.IngressAnnotation {
	return ipdenylist{
		r:                r,
		annotationConfig: denylistAnnotations,
	}
}

// ParseAnnotations parses the annotations contained in the ingress
// rule used to limit access to certain client addresses or networks.
// Multiple ranges can specified using commas as separator
// e.g. `18.0.0.0/8,56.0.0.0/8`
func (a ipdenylist) Parse(ing *networking.Ingress) (interface{}, error) {
	defBackend := a.r.GetDefaultBackend()

	defaultDenylistSourceRange := make([]string, len(defBackend.DenylistSourceRange))
	copy(defaultDenylistSourceRange, defBackend.DenylistSourceRange)
	sort.Strings(defaultDenylistSourceRange)

	val, err := parser.GetStringAnnotation(ipDenylistAnnotation, ing, a.annotationConfig.Annotations)
	if err != nil {
		if err == ing_errors.ErrMissingAnnotations {
			return &SourceRange{CIDR: defaultDenylistSourceRange}, nil
		}

		return &SourceRange{CIDR: defaultDenylistSourceRange}, ing_errors.LocationDenied{
			Reason: err,
		}

	}

	values := strings.Split(val, ",")
	ipnets, ips, err := net.ParseIPNets(values...)
	if err != nil && len(ips) == 0 {
		return &SourceRange{CIDR: defaultDenylistSourceRange}, ing_errors.LocationDenied{
			Reason: fmt.Errorf("the annotation does not contain a valid IP address or network: %w", err),
		}
	}

	cidrs := []string{}
	for k := range ipnets {
		cidrs = append(cidrs, k)
	}
	for k := range ips {
		cidrs = append(cidrs, k)
	}

	sort.Strings(cidrs)

	return &SourceRange{cidrs}, nil
}

func (a ipdenylist) GetDocumentation() parser.AnnotationFields {
	return a.annotationConfig.Annotations
}

func (a ipdenylist) Validate(anns map[string]string) error {
	maxrisk := parser.StringRiskToRisk(a.r.GetSecurityConfiguration().AnnotationsRiskLevel)
	return parser.CheckAnnotationRisk(anns, maxrisk, denylistAnnotations.Annotations)
}
