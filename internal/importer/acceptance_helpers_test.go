package importer_test

import "os"

func osWriteFile(path, s string) error {
	return os.WriteFile(path, []byte(s), 0o644)
}
