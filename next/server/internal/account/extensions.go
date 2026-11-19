package account

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func extensionKeys(bt core.AccountTypeBinding) []string {
	if len(bt.Type.ExtensionsSchema) == 0 {
		return nil
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(bt.Type.ExtensionsSchema, &schema) != nil {
		return nil
	}
	keys := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// mergeExtensions applies the caller's extension values (settings) to the
// stored settings base and validates the resulting extensions against the
// type's extensionsSchema.
func (s *Service) mergeExtensions(bt core.AccountTypeBinding, base, patch []byte) ([]byte, error) {
	if len(bt.Type.ExtensionsSchema) == 0 {
		if len(patch) > 0 {
			return nil, core.InvalidFields(core.FieldError{Field: "settings", Code: "unsupported", Message: "The account type declares no extensions"})
		}
		return base, nil
	}
	settings, err := parseObject(base)
	if err != nil {
		return nil, err
	}
	keys := extensionKeys(bt)
	if len(patch) > 0 {
		var changes fields
		if len(patch) > 65536 || json.Unmarshal(patch, &changes) != nil || changes == nil {
			return nil, core.InvalidFields(core.FieldError{Field: "settings", Code: "invalid", Message: "Settings must be a bounded JSON object"})
		}
		for key, value := range changes {
			if !slices.Contains(keys, key) {
				return nil, core.InvalidFields(core.FieldError{Field: "settings." + key, Code: "unsupported", Message: "Field is not a declared extension"})
			}
			settings[key] = value
		}
	}
	extensions := fields{}
	for _, key := range keys {
		if value, ok := settings[key]; ok {
			extensions[key] = value
		}
	}
	if err := s.validateExtensions(bt, mustJSON(extensions)); err != nil {
		return nil, err
	}
	return mustJSON(settings), nil
}

// compiled is the compiled extensionsSchema, cached by its digest.
func (s *Service) compiled(schema []byte) (*jsonschema.Schema, error) {
	sum := sha256.Sum256(schema)
	key := hex.EncodeToString(sum[:])
	if v, ok := s.schemas.Load(key); ok {
		return v.(*jsonschema.Schema), nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	loc := "https://sub2api.invalid/extensions/" + key + ".json"
	if err := c.AddResource(loc, doc); err != nil {
		return nil, err
	}
	sch, err := c.Compile(loc)
	if err != nil {
		return nil, err
	}
	s.schemas.Store(key, sch)
	return sch, nil
}

func (s *Service) validateExtensions(bt core.AccountTypeBinding, raw []byte) error {
	schema, err := s.compiled(bt.Type.ExtensionsSchema)
	if err != nil {
		return core.ErrPluginUnavailable.WithMessage("Invalid extension schema").WithCause(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err == nil {
		err = schema.Validate(value)
	}
	if err != nil {
		return core.InvalidFields(core.FieldError{Field: "settings", Code: "invalid", Message: "Settings do not match the extension schema"})
	}
	return nil
}
