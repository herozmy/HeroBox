package logs

import "context"

// context key.
type contextKey string

const bufferKey contextKey = "herobox.logbuffer"

// WithBuffer 在 ctx 中注入日志缓冲器。
func WithBuffer(ctx context.Context, buf *Buffer) context.Context {
	return context.WithValue(ctx, bufferKey, buf)
}

// FromContext 从 ctx 中提取日志缓冲器。
func FromContext(ctx context.Context) *Buffer {
	if buf, ok := ctx.Value(bufferKey).(*Buffer); ok {
		return buf
	}
	return nil
}
