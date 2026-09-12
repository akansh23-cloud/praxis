/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// These are the negative tests of playbook Session 3.1 task "structural
// secret exclusion". The claim under test is not "secrets are filtered
// out" but "the evidence path has no way to ask for a Secret at all":
//
//	code that asks the evidence Reader for a Secret DOES NOT COMPILE —
//	Reader has no Secrets method and no generic Get/List through which an
//	arbitrary type could be requested.
//
// A compilation failure cannot itself be a passing test, so these tests
// pin the property the compile-time guarantee rests on: the exact method
// set of the Reader seam and of its production implementation. Any new
// method — a Secrets getter included — fails here until it is added
// deliberately, in the open. TestRBACGrantsNoSecretAccess (rbac_test.go)
// asserts the runtime half: no manifest grants the manager secrets access.

// readerMethods is the pinned, closed surface of the evidence client.
var readerMethods = []string{
	"ListDaemonSets",
	"ListDeployments",
	"ListEvents",
	"ListPods",
	"ListReplicaSets",
	"ListStatefulSets",
}

func methodNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumMethod())
	for i := range t.NumMethod() {
		names = append(names, t.Method(i).Name)
	}
	return names
}

func TestReaderMethodSetIsClosed(t *testing.T) {
	iface := reflect.TypeOf((*Reader)(nil)).Elem()
	if got := methodNames(iface); !reflect.DeepEqual(got, readerMethods) {
		t.Errorf("Reader interface method set drifted:\n got %v\nwant %v", got, readerMethods)
	}
	// The production implementation must add nothing beyond the seam: a
	// helper method on the concrete type would be reachable via type
	// assertion even though the interface hides it.
	impl := reflect.TypeOf(NewReader(nil))
	if got := methodNames(impl); !reflect.DeepEqual(got, readerMethods) {
		t.Errorf("clusterReader method set drifted:\n got %v\nwant %v", got, readerMethods)
	}
}

func TestReaderMentionsNoSecretAnywhere(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf((*Reader)(nil)).Elem(),
		reflect.TypeOf(NewReader(nil)),
	} {
		for i := range typ.NumMethod() {
			m := typ.Method(i)
			if strings.Contains(m.Name, "Secret") {
				t.Errorf("%v exposes a secret-named method %q", typ, m.Name)
			}
			// The signature string spells every parameter and result type
			// (e.g. "func(context.Context, string) ([]v1.Pod, error)"), so
			// a Secret appearing anywhere in a method's types shows here.
			if sig := m.Type.String(); strings.Contains(sig, "Secret") {
				t.Errorf("%v method %s mentions Secret in its signature %q", typ, m.Name, sig)
			}
		}
	}
}

// TestReaderIsNoGenericClientAndNoWriter proves the two escape hatches are
// closed: the wrapper satisfies neither the generic reader interface (whose
// Get/List accept any registered type, Secrets included) nor any writer
// interface, and it cannot be asserted into a secret-getter shape.
func TestReaderIsNoGenericClientAndNoWriter(t *testing.T) {
	var r any = NewReader(nil)

	if _, ok := r.(client.Reader); ok {
		t.Error("evidence Reader satisfies client.Reader — a generic Get/List would reach Secrets")
	}
	if _, ok := r.(client.Writer); ok {
		t.Error("evidence Reader satisfies client.Writer — the evidence path must hold no write methods")
	}
	if _, ok := r.(interface {
		Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
	}); ok {
		t.Error("evidence Reader exposes a generic Get")
	}
	if _, ok := r.(interface {
		GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)
	}); ok {
		t.Error("evidence Reader exposes a Secrets getter")
	}
	for _, verb := range []string{"Create", "Update", "Patch", "Delete", "Apply"} {
		impl := reflect.TypeOf(r)
		if _, ok := impl.MethodByName(verb); ok {
			t.Errorf("evidence Reader exposes write method %s", verb)
		}
	}
}
