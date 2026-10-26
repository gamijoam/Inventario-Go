package logging

import (
	"context"
	"log/slog"
)

// messageRedactor redacta el mensaje del registro, que ReplaceAttr no
// recibe como atributo editable antes de formatearse.
type messageRedactor struct {
	slog.Handler
}

func (h *messageRedactor) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, RedactText(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(a)
		return true
	})
	return h.Handler.Handle(ctx, clean)
}

func (h *messageRedactor) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &messageRedactor{h.Handler.WithAttrs(attrs)}
}

func (h *messageRedactor) WithGroup(name string) slog.Handler {
	return &messageRedactor{h.Handler.WithGroup(name)}
}
