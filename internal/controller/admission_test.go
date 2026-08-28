/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// These tables prove FR-P1-01: every CRD schema rule and CEL validation on
// RemediationPlan is enforced by the API server itself — the same admission
// code path a production cluster runs, reached here through a real envtest
// apiserver. No Go validation function is called anywhere in this file; a
// plan the schema forbids must fail to EXIST, not merely fail a check.
//
// The exact CEL messages are asserted deliberately. They are part of the
// security surface (demos and humans read them), so a reworded or vanished
// rule fails a test instead of passing silently.

// The CEL messages under test, verbatim from api/v1alpha1/remediationplan_types.go.
const (
	msgScaleParamsIff  = "scaleWorkload params must be set iff type is ScaleWorkload"
	msgPatchParamsIff  = "patchResourceLimits params must be set iff type is PatchResourceLimits"
	msgRollbackOnly    = "rollbackRelease params are only valid for type RollbackRelease"
	msgCordonNodeOnly  = "CordonNode must target a Node; no other action may"
	msgNamespaceIff    = "namespace is required for namespaced kinds and forbidden for Node"
	msgMemoryOrCPU     = "at least one of memory or cpu must be set"
	msgOnFailureNeeds  = "onFailure Rollback requires a rollback strategy"
	msgSpecImmutable   = "RemediationPlan spec is immutable; create a new plan instead of editing"
	msgOutOfVocabulary = `Unsupported value: "DeleteNamespace"`
)

// Action builders. Each returns a fully valid action of its flavor; entries
// mutate copies to produce exactly one violation.

func restartAction() praxisv1alpha1.Action {
	return praxisv1alpha1.Action{
		Type: praxisv1alpha1.ActionRestartWorkload,
		Target: praxisv1alpha1.TargetRef{
			Kind: targetKindDeployment, Namespace: testNamespace, Name: fixtureWorkloadName,
		},
	}
}

func scaleAction() praxisv1alpha1.Action {
	return praxisv1alpha1.Action{
		Type: praxisv1alpha1.ActionScaleWorkload,
		Target: praxisv1alpha1.TargetRef{
			Kind: targetKindDeployment, Namespace: testNamespace, Name: fixtureWorkloadName,
		},
		ScaleWorkload: &praxisv1alpha1.ScaleWorkloadParams{FromReplicas: 2, ToReplicas: 4},
	}
}

func rollbackAction() praxisv1alpha1.Action {
	revision := int64(3)
	return praxisv1alpha1.Action{
		Type: praxisv1alpha1.ActionRollbackRelease,
		Target: praxisv1alpha1.TargetRef{
			Kind: targetKindDeployment, Namespace: testNamespace, Name: fixtureWorkloadName,
		},
		RollbackRelease: &praxisv1alpha1.RollbackReleaseParams{ToRevision: &revision},
	}
}

func patchLimitsAction() praxisv1alpha1.Action {
	return praxisv1alpha1.Action{
		Type: praxisv1alpha1.ActionPatchResourceLimits,
		Target: praxisv1alpha1.TargetRef{
			Kind: targetKindDeployment, Namespace: testNamespace, Name: fixtureWorkloadName,
		},
		PatchResourceLimits: &praxisv1alpha1.PatchResourceLimitsParams{
			Container: fixtureContainerName,
			Memory: &praxisv1alpha1.QuantityChange{
				From: resource.MustParse("256Mi"), To: resource.MustParse("512Mi"),
			},
		},
	}
}

func cordonAction() praxisv1alpha1.Action {
	return praxisv1alpha1.Action{
		Type:   praxisv1alpha1.ActionCordonNode,
		Target: praxisv1alpha1.TargetRef{Kind: targetKindNode, Name: fixtureNodeName},
	}
}

// admissionPlan is a valid plan carrying the given actions; mutate hooks in
// each table entry then break exactly one rule.
func admissionPlan(name string, actions ...praxisv1alpha1.Action) *praxisv1alpha1.RemediationPlan {
	plan := newTestPlan(name, "admission-fixture-incident", evidenceHash)
	plan.Spec.Actions = actions
	return plan
}

var _ = Describe("RemediationPlan admission through the API server (FR-P1-01)", func() {
	DescribeTable("plans the schema forbids are rejected at admission and never persist",
		func(build func(name string) *praxisv1alpha1.RemediationPlan, wantMessage string) {
			plan := build(uniqueName("forbidden"))

			err := k8sClient.Create(ctx, plan)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue(),
				"rejection must be the API server's Invalid verdict, got: %v", err)
			Expect(err.Error()).To(ContainSubstring(wantMessage))

			// The object must not exist in any form: rejected means never
			// persisted, not persisted-then-flagged.
			got := &praxisv1alpha1.RemediationPlan{}
			getErr := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: plan.Name}, got)
			Expect(apierrors.IsNotFound(getErr)).To(BeTrue(),
				"rejected plan %q must not be persisted", plan.Name)
		},

		// Closed vocabulary: a verb outside the enum cannot become an object.
		Entry("an out-of-vocabulary action type (DeleteNamespace)",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := restartAction()
				action.Type = praxisv1alpha1.ActionType("DeleteNamespace")
				return admissionPlan(name, action)
			}, msgOutOfVocabulary),
		Entry("an out-of-vocabulary action hidden behind a valid first action",
			func(name string) *praxisv1alpha1.RemediationPlan {
				smuggled := restartAction()
				smuggled.Type = praxisv1alpha1.ActionType("DeleteNamespace")
				return admissionPlan(name, restartAction(), smuggled)
			}, msgOutOfVocabulary),

		// Discriminated union: RestartWorkload takes no parameter block.
		Entry("RestartWorkload carrying scaleWorkload params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := restartAction()
				action.ScaleWorkload = &praxisv1alpha1.ScaleWorkloadParams{FromReplicas: 2, ToReplicas: 4}
				return admissionPlan(name, action)
			}, msgScaleParamsIff),
		Entry("RestartWorkload carrying patchResourceLimits params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := restartAction()
				action.PatchResourceLimits = patchLimitsAction().PatchResourceLimits
				return admissionPlan(name, action)
			}, msgPatchParamsIff),
		Entry("RestartWorkload carrying rollbackRelease params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := restartAction()
				action.RollbackRelease = &praxisv1alpha1.RollbackReleaseParams{}
				return admissionPlan(name, action)
			}, msgRollbackOnly),

		// Discriminated union: ScaleWorkload requires exactly its own block.
		Entry("ScaleWorkload missing its scaleWorkload params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := scaleAction()
				action.ScaleWorkload = nil
				return admissionPlan(name, action)
			}, msgScaleParamsIff),
		Entry("ScaleWorkload also carrying patchResourceLimits params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := scaleAction()
				action.PatchResourceLimits = patchLimitsAction().PatchResourceLimits
				return admissionPlan(name, action)
			}, msgPatchParamsIff),

		// Discriminated union: RollbackRelease params are exclusive to it,
		// and it must not borrow another verb's block.
		Entry("RollbackRelease also carrying scaleWorkload params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := rollbackAction()
				action.ScaleWorkload = &praxisv1alpha1.ScaleWorkloadParams{FromReplicas: 2, ToReplicas: 4}
				return admissionPlan(name, action)
			}, msgScaleParamsIff),

		// Discriminated union: PatchResourceLimits requires its block, and
		// the block must change at least one resource.
		Entry("PatchResourceLimits missing its patchResourceLimits params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := patchLimitsAction()
				action.PatchResourceLimits = nil
				return admissionPlan(name, action)
			}, msgPatchParamsIff),
		Entry("PatchResourceLimits with neither memory nor cpu",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := patchLimitsAction()
				action.PatchResourceLimits = &praxisv1alpha1.PatchResourceLimitsParams{Container: fixtureContainerName}
				return admissionPlan(name, action)
			}, msgMemoryOrCPU),

		// Node pairing: CordonNode targets Node, nothing else does.
		Entry("CordonNode targeting a Deployment",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := cordonAction()
				action.Target = praxisv1alpha1.TargetRef{
					Kind: targetKindDeployment, Namespace: testNamespace, Name: fixtureWorkloadName,
				}
				return admissionPlan(name, action)
			}, msgCordonNodeOnly),
		Entry("CordonNode carrying scaleWorkload params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := cordonAction()
				action.ScaleWorkload = &praxisv1alpha1.ScaleWorkloadParams{FromReplicas: 2, ToReplicas: 4}
				return admissionPlan(name, action)
			}, msgScaleParamsIff),
		Entry("RestartWorkload targeting a Node",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := restartAction()
				action.Target = praxisv1alpha1.TargetRef{Kind: targetKindNode, Name: fixtureNodeName}
				return admissionPlan(name, action)
			}, msgCordonNodeOnly),
		Entry("ScaleWorkload targeting a Node",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := scaleAction()
				action.Target = praxisv1alpha1.TargetRef{Kind: targetKindNode, Name: fixtureNodeName}
				return admissionPlan(name, action)
			}, msgCordonNodeOnly),

		// Namespace pairing: required for namespaced kinds, forbidden for Node.
		Entry("a Deployment target without a namespace",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := restartAction()
				action.Target.Namespace = ""
				return admissionPlan(name, action)
			}, msgNamespaceIff),
		Entry("a StatefulSet target without a namespace",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := restartAction()
				action.Target = praxisv1alpha1.TargetRef{Kind: "StatefulSet", Name: "fixture-db"}
				return admissionPlan(name, action)
			}, msgNamespaceIff),
		Entry("a Node target with a namespace",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := cordonAction()
				action.Target.Namespace = testNamespace
				return admissionPlan(name, action)
			}, msgNamespaceIff),

		// onFailure ⇒ rollback strategy: promising a rollback with no way
		// to perform one is a contradiction the schema refuses.
		Entry("onFailure Rollback with rollback strategy None",
			func(name string) *praxisv1alpha1.RemediationPlan {
				plan := admissionPlan(name, restartAction())
				plan.Spec.Verification.OnFailure = praxisv1alpha1.FailureActionRollback
				plan.Spec.Rollback.Strategy = praxisv1alpha1.RollbackNone
				return plan
			}, msgOnFailureNeeds),
	)

	DescribeTable("valid plans of every action flavor are admitted — the rules are iff, not blanket bans",
		func(build func(name string) *praxisv1alpha1.RemediationPlan) {
			plan := build(uniqueName("admitted"))
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			got := &praxisv1alpha1.RemediationPlan{}
			Expect(k8sClient.Get(ctx,
				types.NamespacedName{Namespace: testNamespace, Name: plan.Name}, got)).To(Succeed())
		},
		Entry("RestartWorkload with no parameter block",
			func(name string) *praxisv1alpha1.RemediationPlan {
				return admissionPlan(name, restartAction())
			}),
		Entry("ScaleWorkload with its scaleWorkload params",
			func(name string) *praxisv1alpha1.RemediationPlan {
				return admissionPlan(name, scaleAction())
			}),
		Entry("RollbackRelease with a pinned revision",
			func(name string) *praxisv1alpha1.RemediationPlan {
				return admissionPlan(name, rollbackAction())
			}),
		Entry("RollbackRelease with params omitted (they are optional for its own type)",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := rollbackAction()
				action.RollbackRelease = nil
				return admissionPlan(name, action)
			}),
		Entry("PatchResourceLimits changing memory only",
			func(name string) *praxisv1alpha1.RemediationPlan {
				return admissionPlan(name, patchLimitsAction())
			}),
		Entry("PatchResourceLimits changing cpu only",
			func(name string) *praxisv1alpha1.RemediationPlan {
				action := patchLimitsAction()
				action.PatchResourceLimits = &praxisv1alpha1.PatchResourceLimitsParams{
					Container: fixtureContainerName,
					CPU: &praxisv1alpha1.QuantityChange{
						From: resource.MustParse("500m"), To: resource.MustParse("1"),
					},
				}
				return admissionPlan(name, action)
			}),
		Entry("CordonNode targeting a Node without a namespace",
			func(name string) *praxisv1alpha1.RemediationPlan {
				return admissionPlan(name, cordonAction())
			}),
		Entry("onFailure Rollback paired with strategy RestorePreviousSpec",
			func(name string) *praxisv1alpha1.RemediationPlan {
				plan := admissionPlan(name, restartAction())
				plan.Spec.Verification.OnFailure = praxisv1alpha1.FailureActionRollback
				plan.Spec.Rollback.Strategy = praxisv1alpha1.RollbackRestorePreviousSpec
				return plan
			}),
	)

	Describe("spec immutability after admission", func() {
		// createAdmitted persists a fresh valid plan and returns it re-read,
		// so each entry mutates live server state, not a stale local copy.
		createAdmitted := func() *praxisv1alpha1.RemediationPlan {
			GinkgoHelper()
			plan := admissionPlan(uniqueName("frozen"), restartAction())
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			Expect(k8sClient.Get(ctx,
				types.NamespacedName{Namespace: testNamespace, Name: plan.Name}, plan)).To(Succeed())
			return plan
		}

		DescribeTable("every spec edit is refused with the immutability message",
			func(mutate func(*praxisv1alpha1.RemediationPlan)) {
				plan := createAdmitted()
				specBefore := plan.Spec.DeepCopy()

				mutate(plan)
				err := k8sClient.Update(ctx, plan)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue(),
					"rejection must be the API server's Invalid verdict, got: %v", err)
				Expect(err.Error()).To(ContainSubstring(msgSpecImmutable))

				// The persisted spec must be byte-for-byte what was admitted.
				got := &praxisv1alpha1.RemediationPlan{}
				Expect(k8sClient.Get(ctx,
					types.NamespacedName{Namespace: testNamespace, Name: plan.Name}, got)).To(Succeed())
				Expect(got.Spec).To(Equal(*specBefore))
			},
			Entry("changing the evidence bundle hash", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.EvidenceBundleHash = differentEvidenceHash
			}),
			Entry("re-pointing the incident reference", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.IncidentRef.Name = "some-other-incident"
			}),
			Entry("rewording the hypothesis", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.Hypothesis.Summary = "a different story about the same evidence"
			}),
			Entry("inflating the confidence", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.Hypothesis.ConfidencePercent = 99
			}),
			Entry("re-aiming an action at another target", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.Actions[0].Target.Name = "some-other-workload"
			}),
			Entry("appending a second action", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.Actions = append(plan.Spec.Actions, scaleAction())
			}),
			Entry("softening the verification predicate", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.Verification.Predicate = "vector(1)"
			}),
			Entry("shrinking the verification window", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.Verification.Window = metav1.Duration{Duration: time.Second}
			}),
			Entry("switching the rollback strategy", func(plan *praxisv1alpha1.RemediationPlan) {
				plan.Spec.Rollback.Strategy = praxisv1alpha1.RollbackRestorePreviousSpec
			}),
		)

		It("metadata and status stay writable — the approval surface depends on exactly that split", func() {
			plan := createAdmitted()

			By("annotating the frozen plan (the Phase 1 approval channel)")
			plan.Annotations = map[string]string{praxisv1alpha1.AnnotationApprove: evidenceHash}
			Expect(k8sClient.Update(ctx, plan)).To(Succeed())

			By("writing status through the status subresource")
			Expect(k8sClient.Get(ctx,
				types.NamespacedName{Namespace: testNamespace, Name: plan.Name}, plan)).To(Succeed())
			plan.Status.Phase = praxisv1alpha1.PlanPhasePending
			Expect(k8sClient.Status().Update(ctx, plan)).To(Succeed())
		})
	})
})
