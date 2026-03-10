package lib

import (
	"regexp"
	"strconv"

	"github.com/sirupsen/logrus"
)

// unquotedMsgRe matches msg= followed by an unquoted value (no leading double-quote).
var unquotedMsgRe = regexp.MustCompile(`\bmsg=([^"\s]\S*)`)

// QuotedMsgFormatter wraps logrus.TextFormatter and always quotes the msg field.
type QuotedMsgFormatter struct {
	logrus.TextFormatter
}

func (f *QuotedMsgFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	b, err := f.TextFormatter.Format(entry)
	if err != nil {
		return nil, err
	}
	return unquotedMsgRe.ReplaceAllFunc(b, func(match []byte) []byte {
		val := string(match[4:]) // skip "msg="
		return append([]byte("msg="), []byte(strconv.Quote(val))...)
	}), nil
}
