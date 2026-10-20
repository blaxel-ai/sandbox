package filesystem

import "os"

// WriteOptions controls writes without changing the historical overwrite default.
type WriteOptions struct {
	NoOverwrite bool
}

func writeOptions(options []WriteOptions) WriteOptions {
	if len(options) > 0 {
		return options[0]
	}
	return WriteOptions{}
}

func (o WriteOptions) openFlags() int {
	if o.NoOverwrite {
		// O_EXCL also refuses directories and symlinks, including dangling links.
		return os.O_CREATE | os.O_WRONLY | os.O_EXCL
	}
	return os.O_CREATE | os.O_WRONLY | os.O_TRUNC
}
