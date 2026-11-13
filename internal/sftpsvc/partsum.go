package sftpsvc

import (
	"hash"
	"hash/crc32"
	"io"
)

// continuePartSum is the checksum of a local part about to be written
// further. r is a read-only view of it (the write handle is write-only).
// off is how much of it already stands. A partial resume must still hold
// the bytes this transfer wrote; a replaced file fails here even when it
// reused the inode and the change time.
func continuePartSum(t *Transfer, r io.ReaderAt, off int64) (hash.Hash32, error) {
	h := crc32.NewIEEE()
	if off == 0 {
		return h, nil
	}
	if err := partSumMatches(t, r, off); err != nil {
		return nil, err
	}
	if _, err := io.Copy(h, io.NewSectionReader(r, 0, off)); err != nil {
		return nil, err
	}
	return h, nil
}

func partSumMatches(t *Transfer, r io.ReaderAt, n int64) error {
	if t.partSumN != n {
		return ErrPartChanged
	}
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, io.NewSectionReader(r, 0, n)); err != nil {
		return err
	}
	if h.Sum32() != t.partSum {
		return ErrPartChanged
	}
	return nil
}
