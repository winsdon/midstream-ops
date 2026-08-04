package repository

import "os"

// ensureDir 确保目录存在（等价 mkdir -p）。
func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
