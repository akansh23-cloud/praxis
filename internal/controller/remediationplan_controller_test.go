/*
Copyright 2026.

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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

var _ = Describe("RemediationPlan Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		remediationplan := &praxisv1alpha1.RemediationPlan{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind RemediationPlan")
			err := k8sClient.Get(ctx, typeNamespacedName, remediationplan)
			if err != nil && errors.IsNotFound(err) {
				resource := &praxisv1alpha1.RemediationPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: praxisv1alpha1.RemediationPlanSpec{
						IncidentRef: praxisv1alpha1.IncidentRef{
							Name: "test-incident",
						},
						EvidenceBundleHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
						Hypothesis: praxisv1alpha1.Hypothesis{
							Summary:           "Test workload requires a controlled restart",
							ConfidencePercent: 80,
							Citations: []praxisv1alpha1.EvidenceID{
								"ev/test-evidence",
							},
						},
						Actions: []praxisv1alpha1.Action{
							{
								Type: praxisv1alpha1.ActionRestartWorkload,
								Target: praxisv1alpha1.TargetRef{
									Kind:      "Deployment",
									Namespace: "default",
									Name:      "test-workload",
								},
							},
						},
						Verification: praxisv1alpha1.VerificationSpec{
							Predicate: "up == 1",
							Window:    metav1.Duration{},
							OnFailure: praxisv1alpha1.FailureActionEscalate,
						},
						Rollback: praxisv1alpha1.RollbackSpec{
							Strategy: praxisv1alpha1.RollbackNone,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &praxisv1alpha1.RemediationPlan{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance RemediationPlan")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &RemediationPlanReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
