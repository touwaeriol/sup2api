package gateway

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

type modelReferenceLocation struct {
	path string
	item gjson.Result
}

// modelReferenceLocationsIn finds the model references of one rule in a parsed
// body. Only declared array hops are followed. Tool input, prompt text, and
// arbitrary object trees are never searched for keys named "model". The
// request path parses once per body version (call.bodyRoot) and hands the root
// in: parsing copies the body, and a request runs several rules.
func modelReferenceLocationsIn(root gjson.Result, arrayPath string) ([]modelReferenceLocation, error) {
	nodes := []modelReferenceLocation{{item: root}}
	parts := strings.Split(arrayPath, ".")
	nested := strings.Contains(arrayPath, "[]")
	for index, part := range parts {
		enumerate := strings.HasSuffix(part, "[]") || index == len(parts)-1
		field := strings.TrimSuffix(part, "[]")
		var next []modelReferenceLocation
		for _, node := range nodes {
			value := node.item.Get(field)
			if !value.Exists() || value.Type == gjson.Null {
				continue
			}
			path := field
			if node.path != "" {
				path = node.path + "." + field
			}
			if !enumerate {
				next = append(next, modelReferenceLocation{path, value})
				continue
			}
			if !value.IsArray() {
				// Nested protocol unions include shorthand string content. They
				// cannot contain model-bearing objects; normal schema admission
				// still owns all other message validation.
				if nested && value.Type == gjson.String {
					continue
				}
				return nil, fmt.Errorf("%s must be an array", path)
			}
			for offset, item := range value.Array() {
				if len(next) >= 16384 {
					return nil, errors.New("model reference traversal exceeds limit")
				}
				next = append(next, modelReferenceLocation{path + "." + strconv.Itoa(offset), item})
			}
		}
		nodes = next
	}
	return nodes, nil
}
