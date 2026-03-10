package lib

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// unquotedAuditFieldRe matches top-level msg= or command= fields followed by
// an unquoted value. The lookbehind-like anchor (^ or space) ensures we only
// match at the field level, not inside an already-quoted msg value where
// "command=foo" could appear as part of the message text.
var unquotedAuditFieldRe = regexp.MustCompile(`(?:^| )(msg|command)=([^"\s]\S*)`)

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
		// Preserve leading space if present (the anchor captures it).
		prefix := ""
		if s[0] == ' ' {
			prefix = " "
			s = s[1:]
		}
		eqIdx := strings.IndexByte(s, '=')
		return []byte(prefix + s[:eqIdx+1] + strconv.Quote(s[eqIdx+1:]))
	}), nil
}
