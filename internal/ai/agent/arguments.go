package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Licoy/sshyard/internal/ai/llm"
)

// toolArguments validates the declared tool contract before path resolution,
// diff reads or authorization; omitted content must never turn into an empty write
func toolArguments(name, raw string) (map[string]any, error) {
	return toolArgumentsIn(ToolDefs(), name, raw)
}

// toolArgumentsIn is toolArguments against the function set defs.
func toolArgumentsIn(defs []llm.Tool, name, raw string) (map[string]any, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	for _, def := range defs {
		if def.Name != name {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(def.Parameters, &schema); err != nil {
			return nil, err
		}
		for _, key := range schema.Required {
			if _, ok := fields[key]; !ok {
				return nil, fmt.Errorf("missing tool argument: %s", key)
			}
		}
		result := make(map[string]any, len(fields))
		for key, value := range fields {
			prop, ok := schema.Properties[key]
			if !ok {
				return nil, fmt.Errorf("unknown tool argument: %s", key)
			}
			if string(value) == "null" {
				return nil, fmt.Errorf("invalid tool argument type: %s", key)
			}
			switch prop.Type {
			case "string":
				var text string
				if err := json.Unmarshal(value, &text); err != nil {
					return nil, fmt.Errorf("invalid tool argument type: %s", key)
				}
				if (key == "path" || key == "command" || key == "query" || key == "key") && strings.TrimSpace(text) == "" {
					return nil, fmt.Errorf("empty tool argument: %s", key)
				}
				result[key] = text
			case "integer":
				var number int
				if err := json.Unmarshal(value, &number); err != nil || number <= 0 {
					return nil, fmt.Errorf("invalid positive integer tool argument: %s", key)
				}
				result[key] = float64(number)
			default:
				return nil, errors.New("unsupported tool argument schema")
			}
		}
		return result, nil
	}
	return nil, errors.New("unknown tool")
}
