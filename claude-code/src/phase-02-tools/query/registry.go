package query

import "context"

// NewToolRegistry creates a ToolRegistry with builtins listed before extras.
// Within each group the original order is preserved (stable sort).
func NewToolRegistry(builtins, extras []Tool) *ToolRegistry {
	r := &ToolRegistry{}
	r.builtins = make([]Tool, len(builtins))
	copy(r.builtins, builtins)
	r.extras = make([]Tool, len(extras))
	copy(r.extras, extras)
	return r
}

// FindByName returns the first tool with the given name, searching builtins
// before extras.
func (r *ToolRegistry) FindByName(name string) (Tool, bool) {
	for _, t := range r.builtins {
		if t.Name() == name {
			return t, true
		}
	}
	for _, t := range r.extras {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// All returns all registered tools (builtins first, then extras).
func (r *ToolRegistry) All() []Tool {
	out := make([]Tool, 0, len(r.builtins)+len(r.extras))
	out = append(out, r.builtins...)
	out = append(out, r.extras...)
	return out
}

// ToDefinitions returns a ToolDefinition slice suitable for passing to the API.
func (r *ToolRegistry) ToDefinitions() []ToolDefinition {
	all := r.All()
	defs := make([]ToolDefinition, 0, len(all))
	for _, t := range all {
		defs = append(defs, ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(nil),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// ExecuteTool runs the validate → permission → execute → result-wrap pipeline.
// It returns an error only when the pipeline itself fails (not when the tool
// returns IsError=true content).
func ExecuteTool(ctx context.Context, t Tool, input map[string]any, tctx *ToolUseContext) (ToolResult, error) {
	// 1. Validate
	vr := t.ValidateInput(input, tctx)
	if !vr.OK {
		return ToolResult{}, &ValidationError{Message: vr.Message}
	}

	// 2. Permission check
	pd := t.CheckPermissions(input, tctx)
	switch pd.Behavior {
	case PermDeny:
		return ToolResult{
			Content: "Permission denied: " + pd.Message,
			IsError: true,
		}, nil
	case PermAsk:
		// Phase 2: auto-allow for "ask" (interactive permission UI is Phase 6+)
	}

	// 3. Execute
	result, err := t.Call(ctx, input, tctx, nil)
	if err != nil {
		return ToolResult{
			Content: err.Error(),
			IsError: true,
		}, nil
	}

	// 4. Result wrap (already wrapped by Call)
	return result, nil
}

// ValidationError is returned by ExecuteTool when validation fails.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return "validation failed: " + e.Message
}
