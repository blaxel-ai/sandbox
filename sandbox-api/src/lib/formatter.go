package lib

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// unquotedAuditFieldRe matches msg= or command= followed by an unquoted value
// (no leading double-quote). Logrus quotes multi-word values automatically but
// leaves single-word values unquoted; this regex catches the single-word case.
var unquotedAuditFieldRe = regexp.MustCompile(`\b(msg|command)=([^"\s]\S*)`)

// QuotedMsgFormatter wraps logrus.TextFormatter and always quotes the msg and
// command field values, making the log format predictable for regex-based
// parsers (e.g. SigNoz).
type QuotedMsgFormatter struct {
	logrus.TextFormatter
}

func (f *QuotedMsgFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	b, err := f.TextFormatter.Format(entry)
	if err != nil {
		return nil, err
	}
	return unquotedAuditFieldRe.ReplaceAllFunc(b, func(match []byte) []byte {
		s := string(match)
		eqIdx := strings.IndexByte(s, '=')
		return []byte(s[:eqIdx+1] + strconv.Quote(s[eqIdx+1:]))
	}), nil
}
