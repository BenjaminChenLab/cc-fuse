package store_test

import "os"

func osWriteImpl(path, s string) error {
	return os.WriteFile(path, []byte(s), 0o644)
}
