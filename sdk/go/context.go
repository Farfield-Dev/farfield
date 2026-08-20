package farfield

import "context"

type Scope struct {
	ConversationID string
	TraceID        string
	SpanID         string
	ParentID       string
	Agent          string
	Tags           map[string]string
}

type scopeKey struct{}

// WithScope returns a child context whose defaults are applied to Capture.
// Explicit fields on CaptureInput take precedence over scoped values.
func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, cloneScope(scope))
}

func WithConversation(ctx context.Context, conversationID string) context.Context {
	current, _ := ScopeFromContext(ctx)
	current.ConversationID = conversationID
	return WithScope(ctx, current)
}

func ScopeFromContext(ctx context.Context) (Scope, bool) {
	value, ok := ctx.Value(scopeKey{}).(Scope)
	return cloneScope(value), ok
}

func cloneScope(value Scope) Scope {
	value.Tags = cloneTags(value.Tags)
	return value
}
