package lib

import (
	"bytes"
	"fmt"

	"github.com/sirupsen/logrus"
)

// QuotedMsgFormatter wraps logrus.TextFormatter but always quotes the msg field.
type QuotedMsgFormatter struct {
	logrus.TextFormatter
}

func (f *QuotedMsgFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// Temporarily replace the message with a sentinel, format, then swap it back quoted.
	original := entry.Message
	// Use a placeholder that won't appear in real messages.
	const placeholder = "\x00MSGPLACEHOLDER\x00"
	entry.Message = placeholder
	b, err := f.TextFormatter.Format(entry)
	entry.Message = original
	if err != nil {
		return nil, err
	}
	// Replace placeholder (unquoted or quoted by logrus) with the properly quoted original.
	quoted := fmt.Sprintf("%q", original)
	result := bytes.Replace(b, []byte(`msg=`+placeholder), []byte(`msg=`+quoted), 1)
	// Also handle the case where logrus already quoted the placeholder.
	result = bytes.Replace(result, []byte(`msg="`+placeholder+`"`), []byte(`msg=`+quoted), 1)
	return result, nil
}
