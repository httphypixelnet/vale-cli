package system

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxUnarchivedFile bounds what a single archived file may expand to, so that
// a small archive cannot ask for an unbounded write.
const maxUnarchivedFile = 10 * 1024 * 1024 * 1024

// Unarchive extracts a ZIP archive to a destination directory.
func Unarchive(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err = Mkdir(dest); err != nil {
		return err
	}

	for _, file := range r.File {
		if err = extract(file, dest); err != nil {
			return err
		}
	}

	return nil
}

// extract writes one archived file into dest.
//
// This is its own function so that the handles it opens are closed as it
// returns, rather than accumulating until the whole archive has been read --
// and so that closing the written file can report a failure. A write is not
// necessarily on the disk until the handle is closed, so discarding that error
// discards the only sign that the file is incomplete.
func extract(file *zip.File, dest string) (err error) {
	destPath := filepath.Join(dest, filepath.Clean(file.Name))
	if !strings.HasPrefix(destPath, filepath.Clean(dest)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path: %s", file.Name)
	}

	if file.FileInfo().IsDir() {
		return Mkdir(destPath)
	}
	if err = Mkdir(filepath.Dir(destPath)); err != nil {
		return err
	}

	srcFile, err := file.Open()
	if err != nil {
		return err
	}
	defer srcFile.Close() //nolint:errcheck // read-only; nothing to lose on close

	dstFile, err := os.OpenFile(
		destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer func() {
		// Reported only when nothing has already gone wrong, so that the first
		// failure is the one the caller sees.
		if cerr := dstFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	_, err = io.Copy(dstFile, io.LimitReader(srcFile, maxUnarchivedFile))

	return err
}
