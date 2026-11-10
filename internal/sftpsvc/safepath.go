package sftpsvc

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
)

var (
	ErrUnsafePath        = errors.New("sftp: symbolic link or unsafe path is not allowed")
	ErrUnsafeDestination = errors.New("sftp: destination folder is writable by everyone")
	ErrPartChanged       = errors.New("sftp: partial file identity changed")
)

// ResolvePath rejects symlinks in every existing component. SFTP v3 has no
// openat/no-follow operation: this is a conservative check, not an atomic
// guarantee against a server changing its namespace concurrently.
func (c *Client) ResolvePath(ctx context.Context, p string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sub, err := c.operationClient(ctx)
	if err != nil {
		return "", err
	}
	defer sub.Close()
	stop := context.AfterFunc(ctx, func() { sub.Close() })
	defer stop()
	p, err = sub.resolvePath(ctx, p)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return p, err
}

func (c *Client) resolvePath(ctx context.Context, p string) (string, error) {
	if strings.ContainsRune(p, 0) || p == "" {
		return "", errors.New("sftp: invalid path")
	}
	if !path.IsAbs(p) {
		home, err := c.Home()
		if err != nil {
			return "", err
		}
		p = path.Join(home, p)
	}
	p = path.Clean(p)
	cur := "/"
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, name := range parts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		cur = path.Join(cur, name)
		fi, err := c.c.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) && i == len(parts)-1 {
			return p, nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "", ErrUnsafePath
		}
		if i < len(parts)-1 && !fi.IsDir() {
			return "", errors.New("sftp: parent is not a directory")
		}
	}
	return p, nil
}

// checkTransferParent permits missing descendants, but checks every existing
// ancestor before mkdir. create checks the final directory again afterwards.
func (c *Client) checkTransferParent(ctx context.Context, p string) error {
	for {
		_, err := c.ResolvePath(ctx, p)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) || p == path.Dir(p) {
			return err
		}
		p = path.Dir(p)
	}
}
