/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package planschema

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	structuralcel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// The embedded artifacts; regenerate with `make schema-derive`.
var (
	//go:embed remediationplanspec.openapi.json
	specOpenAPI []byte

	//go:embed planner.schema.json
	plannerSchema []byte

	//go:embed hypotheses.schema.json
	hypothesesSchema []byte
)

// SpecOpenAPI returns the RemediationPlanSpec OpenAPI v3 schema, verbatim
// from the CRD.
func SpecOpenAPI() []byte { return slices.Clone(specOpenAPI) }

// PlannerSchema returns the JSON Schema the planner constrains the model
// with.
func PlannerSchema() []byte { return slices.Clone(plannerSchema) }

// HypothesesSchema returns the JSON Schema the hypothesis engine
// constrains the model with.
func HypothesesSchema() []byte { return slices.Clone(hypothesesSchema) }

// validator is the API server's own machinery over the embedded spec
// schema: the structural/OpenAPI validator and the CEL rule validator.
type validator struct {
	schema     validation.SchemaValidator
	structural *structuralschema.Structural
	cel        *structuralcel.Validator
}

var (
	buildOnce sync.Once
	built     *validator
	buildErr  error
)

func load() (*validator, error) {
	buildOnce.Do(func() {
		var v1 apiextensionsv1.JSONSchemaProps
		if err := json.Unmarshal(specOpenAPI, &v1); err != nil {
			buildErr = fmt.Errorf("planschema: parse embedded spec OpenAPI: %w", err)
			return
		}
		var internal apiextensions.JSONSchemaProps
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&v1, &internal, nil); err != nil {
			buildErr = fmt.Errorf("planschema: convert spec OpenAPI: %w", err)
			return
		}
		sv, _, err := validation.NewSchemaValidator(&internal)
		if err != nil {
			buildErr = fmt.Errorf("planschema: build schema validator: %w", err)
			return
		}
		st, err := structuralschema.NewStructural(&internal)
		if err != nil {
			buildErr = fmt.Errorf("planschema: build structural schema: %w", err)
			return
		}
		built = &validator{
			schema:     sv,
			structural: st,
			cel:        structuralcel.NewValidator(st, false, celconfig.PerCallLimit),
		}
	})
	return built, buildErr
}

// ValidateSpec runs the CRD's schema and CEL rules over a spec exactly as
// admission would on create (transition rules, which need an old object,
// do not apply). A nil error means the API server would accept the spec;
// a non-nil error lists every violation, in a deterministic order.
func ValidateSpec(ctx context.Context, spec *praxisv1alpha1.RemediationPlanSpec) error {
	if spec == nil {
		return errors.New("planschema: nil spec")
	}
	v, err := load()
	if err != nil {
		return err
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(spec)
	if err != nil {
		return fmt.Errorf("planschema: convert spec: %w", err)
	}

	var messages []string
	if result := v.schema.Validate(obj); result != nil {
		for _, e := range result.Errors {
			messages = append(messages, e.Error())
		}
	}
	if v.cel != nil {
		errs, _ := v.cel.Validate(ctx, field.NewPath("spec"), v.structural, obj, nil, celconfig.RuntimeCELCostBudget)
		for _, e := range errs {
			messages = append(messages, e.Error())
		}
	}
	if len(messages) == 0 {
		return nil
	}
	slices.Sort(messages)
	return &ValidationError{Violations: messages}
}

// ValidationError lists why a spec would be refused by admission.
type ValidationError struct {
	Violations []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("spec violates the RemediationPlan schema (%d violation(s)): %s",
		len(e.Violations), strings.Join(e.Violations, "; "))
}
