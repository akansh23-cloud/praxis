/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package planschema

import (
	"bytes"
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// Artifacts are the three derived documents, pretty-printed JSON with
// sorted keys so re-derivation of an unchanged CRD is byte-stable.
type Artifacts struct {
	SpecOpenAPI []byte
	Planner     []byte
	Hypotheses  []byte
}

// The CRD version the planner targets.
const crdVersion = "v1alpha1"

// JSON Schema keywords and the field names the projection handles.
const (
	kwType                 = "type"
	kwObject               = "object"
	kwString               = "string"
	kwAdditionalProperties = "additionalProperties"
	kwDescription          = "description"
	kwProperties           = "properties"
	kwRequired             = "required"
	kwItems                = "items"
	kwAnyOf                = "anyOf"
	fieldActions           = "actions"
	fieldPlan              = "plan"
	fieldHypotheses        = "hypotheses"
	fieldHypothesis        = "hypothesis"
)

// The spec fields the model decides. Everything else in
// RemediationPlanSpec is set by code from the incident, the bundle and
// the validated analysis.
var modelOwnedPlanFields = []string{fieldActions, "verification", "rollback"}

// Derive reads a CustomResourceDefinition manifest and derives the
// artifacts. It is pure: identical CRD bytes yield identical artifacts.
func Derive(crdYAML []byte) (*Artifacts, error) {
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(crdYAML, &crd); err != nil {
		return nil, fmt.Errorf("parse CRD: %w", err)
	}
	if crd.Spec.Names.Kind != "RemediationPlan" {
		return nil, fmt.Errorf("CRD is for kind %q, want RemediationPlan", crd.Spec.Names.Kind)
	}
	var spec *apiextensionsv1.JSONSchemaProps
	for i := range crd.Spec.Versions {
		v := &crd.Spec.Versions[i]
		if v.Name != crdVersion || v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			continue
		}
		props, ok := v.Schema.OpenAPIV3Schema.Properties["spec"]
		if !ok {
			return nil, fmt.Errorf("CRD version %s has no spec schema", crdVersion)
		}
		spec = &props
	}
	if spec == nil {
		return nil, fmt.Errorf("CRD has no version %s with an OpenAPI schema", crdVersion)
	}

	specJSON, err := marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("render spec OpenAPI: %w", err)
	}

	// Work on a generic tree from here: the projection is a JSON-level
	// transformation, and a map keeps it independent of the Go types'
	// omitempty choices.
	var tree map[string]any
	if err := json.Unmarshal(specJSON, &tree); err != nil {
		return nil, err
	}

	plan, err := projectPlan(tree)
	if err != nil {
		return nil, err
	}
	planner := map[string]any{
		kwType:                 kwObject,
		kwAdditionalProperties: false,
		kwRequired:             []any{"verdict", "noActionReason", fieldPlan},
		kwDescription: "The planner's verdict for one incident. verdict \"plan\" requires plan; " +
			"verdict \"no-action\" requires noActionReason and a null plan.",
		kwProperties: map[string]any{
			"verdict": map[string]any{
				kwType:        kwString,
				"enum":        []any{fieldPlan, "no-action"},
				kwDescription: "Whether to propose a remediation plan or to explicitly propose no action.",
			},
			"noActionReason": map[string]any{
				kwType:        kwString,
				kwDescription: "One sentence justifying a no-action verdict; empty for a plan.",
			},
			fieldPlan: map[string]any{
				kwAnyOf:       []any{plan, map[string]any{kwType: "null"}},
				kwDescription: "The proposed plan (verdict \"plan\"), or null (verdict \"no-action\").",
			},
		},
	}
	plannerJSON, err := marshal(planner)
	if err != nil {
		return nil, err
	}

	hypothesisProps, ok := nested(tree, kwProperties, fieldHypothesis)
	if !ok {
		return nil, fmt.Errorf("spec schema has no hypothesis property")
	}
	hypothesis, err := toModelSchema(hypothesisProps)
	if err != nil {
		return nil, fmt.Errorf("project hypothesis: %w", err)
	}
	hypotheses := map[string]any{
		kwType:                 kwObject,
		kwAdditionalProperties: false,
		kwRequired:             []any{fieldHypotheses},
		kwDescription: "Ranked root-cause hypotheses, most likely first. Every claim must cite " +
			"evidence ids that exist in the bundle; an empty list means no explanation.",
		kwProperties: map[string]any{
			fieldHypotheses: map[string]any{
				kwType:  "array",
				kwItems: hypothesis,
			},
		},
	}
	hypothesesJSON, err := marshal(hypotheses)
	if err != nil {
		return nil, err
	}
	return &Artifacts{SpecOpenAPI: specJSON, Planner: plannerJSON, Hypotheses: hypothesesJSON}, nil
}

// projectPlan keeps only the model-owned spec fields and converts them to
// the structured-output subset.
func projectPlan(spec map[string]any) (map[string]any, error) {
	props, ok := spec[kwProperties].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("spec schema has no properties")
	}
	out := map[string]any{
		kwType:                 kwObject,
		kwAdditionalProperties: false,
		kwRequired:             toAnySlice(modelOwnedPlanFields),
		kwProperties:           map[string]any{},
	}
	if d, ok := spec[kwDescription]; ok {
		out[kwDescription] = d
	}
	for _, name := range modelOwnedPlanFields {
		p, ok := props[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("spec schema has no %q property", name)
		}
		converted, err := toModelSchema(p)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", name, err)
		}
		out[kwProperties].(map[string]any)[name] = converted
	}
	return out, nil
}

// unsupportedKeywords are OpenAPI/JSON-Schema keywords structured outputs
// reject or ignore; the verbatim OpenAPI keeps them and ValidateSpec
// enforces them.
var unsupportedKeywords = map[string]bool{
	"format": true, "pattern": true,
	"minLength": true, "maxLength": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minItems": true, "maxItems": true, "uniqueItems": true,
	"minProperties": true, "maxProperties": true,
	"nullable": true, "default": true, "example": true,
}

// toModelSchema converts one OpenAPI v3 (structural) schema node into the
// subset structured outputs support: types, enums, descriptions, object
// properties with additionalProperties false, arrays, anyOf. Kubernetes
// extensions are dropped; int-or-string becomes string (a Kubernetes
// quantity is best expressed as its string form).
func toModelSchema(node map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if v, ok := node["x-kubernetes-int-or-string"].(bool); ok && v {
		out[kwType] = kwString
		if d, ok := node[kwDescription]; ok {
			out[kwDescription] = d
		}
		return out, nil
	}
	for k, v := range node {
		switch {
		case unsupportedKeywords[k]:
			continue
		case len(k) > 12 && k[:12] == "x-kubernetes":
			continue
		}
		switch k {
		case kwType, kwDescription, "enum", kwRequired:
			out[k] = v
		case kwProperties:
			props, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("properties is not an object")
			}
			converted := map[string]any{}
			for name, child := range props {
				childNode, ok := child.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("property %s is not an object", name)
				}
				c, err := toModelSchema(childNode)
				if err != nil {
					return nil, fmt.Errorf("property %s: %w", name, err)
				}
				converted[name] = c
			}
			out[kwProperties] = converted
		case kwItems:
			itemNode, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("items is not an object")
			}
			c, err := toModelSchema(itemNode)
			if err != nil {
				return nil, fmt.Errorf("items: %w", err)
			}
			out[kwItems] = c
		case kwAnyOf, "oneOf", "allOf":
			list, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s is not a list", k)
			}
			converted := make([]any, 0, len(list))
			for _, alt := range list {
				altNode, ok := alt.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s entry is not an object", k)
				}
				c, err := toModelSchema(altNode)
				if err != nil {
					return nil, err
				}
				converted = append(converted, c)
			}
			out[k] = converted
		default:
			return nil, fmt.Errorf("unhandled schema keyword %q — extend the derivation deliberately", k)
		}
	}
	if out[kwType] == kwObject {
		out[kwAdditionalProperties] = false
	}
	return out, nil
}

func nested(m map[string]any, path ...string) (map[string]any, bool) {
	cur := m
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func toAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// marshal renders sorted-key, indented JSON with a trailing newline —
// reviewable in a diff, byte-stable across derivations.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
