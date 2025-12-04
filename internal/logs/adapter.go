package logs

import (
    "fmt"
    "log"
)

var defaultBuffer = NewBuffer(300)

// SetBuffer allows overriding the default log buffer.
func SetBuffer(buf *Buffer) {
    if buf == nil {
        return
    }
    defaultBuffer = buf
}

func BufferEntries() []Entry {
    if defaultBuffer == nil {
        return nil
    }
    return defaultBuffer.List()
}

func Infof(format string, args ...any) {
    write("info", format, args...)
}

func Errorf(format string, args ...any) {
    write("error", format, args...)
}

func write(level, format string, args ...any) {
	msg := formatMessage(format, args...)
	log.Print(msg)
    if defaultBuffer != nil {
        defaultBuffer.Add(level, msg)
    }
}

func formatMessage(format string, args ...any) string {
    if len(args) == 0 {
        return format
    }
    return fmt.Sprintf(format, args...)
}
