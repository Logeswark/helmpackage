/*
Copyright 2015 The Kubernetes Authors.

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

package auth

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	api "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/ingress-nginx/internal/ingress/annotations/parser"
	ing_errors "k8s.io/ingress-nginx/internal/ingress/errors"
	"k8s.io/ingress-nginx/internal/ingress/resolver"
)

func buildIngress() *networking.Ingress {
	defaultBackend := networking.IngressBackend{
		Service: &networking.IngressServiceBackend{
			Name: "default-backend",
			Port: networking.ServiceBackendPort{
				Number: 80,
			},
		},
	}

	return &networking.Ingress{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "foo",
			Namespace: api.NamespaceDefault,
		},
		Spec: networking.IngressSpec{
			DefaultBackend: &networking.IngressBackend{
				Service: &networking.IngressServiceBackend{
					Name: "default-backend",
					Port: networking.ServiceBackendPort{
						Number: 80,
					},
				},
			},
			Rules: []networking.IngressRule{
				{
					Host: "foo.bar.com",
					IngressRuleValue: networking.IngressRuleValue{
						HTTP: &networking.HTTPIngressRuleValue{
							Paths: []networking.HTTPIngressPath{
								{
									Path:    "/foo",
									Backend: defaultBackend,
								},
							},
						},
					},
				},
			},
		},
	}
}

type mockSecret struct {
	resolver.Mock
}

func (m mockSecret) GetSecret(name string) (*api.Secret, error) {
	if name != "default/demo-secret" && name != "otherns/demo-secret" {
		return nil, fmt.Errorf("there is no secret with name %v", name)
	}

	ns, _, err := cache.SplitMetaNamespaceKey(name)
	if err != nil {
		return nil, err
	}

	return &api.Secret{
		ObjectMeta: meta_v1.ObjectMeta{
			Namespace: ns,
			Name:      "demo-secret",
		},
		Data: map[string][]byte{"auth": []byte("foo:$apr1$OFG3Xybp$ckL0FHDAkoXYIlH9.cysT0")},
	}, nil
}

func TestIngressWithoutAuth(t *testing.T) {
	ing := buildIngress()
	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)
	_, err := NewParser(dir, &mockSecret{}).Parse(ing)
	if err == nil {
		t.Error("Expected error with ingress without annotations")
	}
}

func TestIngressAuthBadAuthType(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "invalid"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	expected := ing_errors.NewValidationError("nginx.ingress.kubernetes.io/auth-type")
	_, err := NewParser(dir, &mockSecret{}).Parse(ing)
	if err.Error() != expected.Error() {
		t.Errorf("expected '%v' but got '%v'", expected, err)
	}
}

func TestIngressInvalidRealm(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	data[parser.GetAnnotationWithPrefix(authRealmAnnotation)] = "something weird ; location trying to { break }"
	data[parser.GetAnnotationWithPrefix(AuthSecretAnnotation)] = "demo-secret"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	expected := ing_errors.NewValidationError("nginx.ingress.kubernetes.io/auth-realm")
	_, err := NewParser(dir, &mockSecret{}).Parse(ing)
	if err.Error() != expected.Error() {
		t.Errorf("expected '%v' but got '%v'", expected, err)
	}
}

func TestIngressInvalidDifferentNamespace(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	data[parser.GetAnnotationWithPrefix(AuthSecretAnnotation)] = "otherns/demo-secret"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	expected := ing_errors.LocationDenied{
		Reason: errors.New("cross namespace usage of secrets is not allowed"),
	}
	_, err := NewParser(dir, &mockSecret{}).Parse(ing)
	if err.Error() != expected.Error() {
		t.Errorf("expected '%v' but got '%v'", expected, err)
	}
}

func TestIngressInvalidDifferentNamespaceAllowed(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	data[parser.GetAnnotationWithPrefix(AuthSecretAnnotation)] = "otherns/demo-secret"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	r := mockSecret{}
	r.AllowCrossNamespace = true
	_, err := NewParser(dir, r).Parse(ing)
	if err != nil {
		t.Errorf("not expecting an error")
	}
}

func TestIngressInvalidSecretName(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	data[parser.GetAnnotationWithPrefix(AuthSecretAnnotation)] = "demo-secret;xpto"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	expected := ing_errors.LocationDenied{
		Reason: errors.New("error reading secret name from annotation: annotation nginx.ingress.kubernetes.io/auth-secret contains invalid value"),
	}
	_, err := NewParser(dir, &mockSecret{}).Parse(ing)
	if err.Error() != expected.Error() {
		t.Errorf("expected '%v' but got '%v'", expected, err)
	}
}

func TestInvalidIngressAuthNoSecret(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	expected := ing_errors.LocationDenied{
		Reason: errors.New("error reading secret name from annotation: ingress rule without annotations"),
	}
	_, err := NewParser(dir, &mockSecret{}).Parse(ing)
	if err.Error() != expected.Reason.Error() {
		t.Errorf("expected '%v' but got '%v'", expected, err)
	}
}

func TestIngressAuth(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	data[parser.GetAnnotationWithPrefix(AuthSecretAnnotation)] = "demo-secret"
	data[parser.GetAnnotationWithPrefix(authRealmAnnotation)] = "-realm-"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	i, err := NewParser(dir, &mockSecret{}).Parse(ing)
	if err != nil {
		t.Errorf("Unexpected error with ingress: %v", err)
	}
	auth, ok := i.(*Config)
	if !ok {
		t.Errorf("expected a BasicDigest type")
	}
	if auth.Type != "basic" {
		t.Errorf("Expected basic as auth type but returned %s", auth.Type)
	}
	if auth.Realm != "-realm-" {
		t.Errorf("Expected -realm- as realm but returned %s", auth.Realm)
	}
	if !auth.Secured {
		t.Errorf("Expected true as secured but returned %v", auth.Secured)
	}
}

func TestIngressAuthWithoutSecret(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	data[parser.GetAnnotationWithPrefix(AuthSecretAnnotation)] = "invalid-secret"
	data[parser.GetAnnotationWithPrefix(authRealmAnnotation)] = "-realm-"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	_, err := NewParser(dir, mockSecret{}).Parse(ing)
	if err == nil {
		t.Errorf("expected an error with invalid secret name")
	}
}

func TestIngressAuthInvalidSecretKey(t *testing.T) {
	ing := buildIngress()

	data := map[string]string{}
	data[parser.GetAnnotationWithPrefix(authTypeAnnotation)] = "basic"
	data[parser.GetAnnotationWithPrefix(AuthSecretAnnotation)] = "demo-secret"
	data[parser.GetAnnotationWithPrefix(authSecretTypeAnnotation)] = "invalid-type"
	data[parser.GetAnnotationWithPrefix(authRealmAnnotation)] = "-realm-"
	ing.SetAnnotations(data)

	_, dir, _ := dummySecretContent(t)
	defer os.RemoveAll(dir)

	_, err := NewParser(dir, mockSecret{}).Parse(ing)
	if err == nil {
		t.Errorf("expected an error with invalid secret name")
	}
}

func dummySecretContent(t *testing.T) (string, string, *api.Secret) {
	dir, err := os.MkdirTemp("", fmt.Sprintf("%v", time.Now().Unix()))
	if err != nil {
		t.Error(err)
	}

	tmpfile, err := os.CreateTemp("", "example-")
	if err != nil {
		t.Error(err)
	}
	defer tmpfile.Close()
	s, _ := mockSecret{}.GetSecret("default/demo-secret")
	return tmpfile.Name(), dir, s
}

func TestDumpSecretAuthFile(t *testing.T) {
	tmpfile, dir, s := dummySecretContent(t)
	defer os.RemoveAll(dir)

	sd := s.Data
	s.Data = nil

	err := dumpSecretAuthFile(tmpfile, s)
	if err == nil {
		t.Errorf("Expected error with secret without auth")
	}

	s.Data = sd
	err = dumpSecretAuthFile(tmpfile, s)
	if err != nil {
		t.Errorf("Unexpected error creating htpasswd file %v: %v", tmpfile, err)
	}
}

func TestDumpSecretAuthMap(t *testing.T) {
	tmpfile, dir, s := dummySecretContent(t)
	defer os.RemoveAll(dir)

	err := dumpSecretAuthMap(tmpfile, s)
	if err != nil {
		t.Errorf("Unexpected error creating htpasswd file %v: %v", tmpfile, err)
	}
}
