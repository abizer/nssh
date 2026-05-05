//go:build !darwin

// Package clipboard wraps the local-machine clipboard for the nssh session
// wrapper. Today only macOS is implemented; on other platforms every call
// returns errUnsupported. See task #13 for Linux client support.
package clipboard

import "errors"

var errUnsupported = errors.New("clipboard: only macOS is supported as a local nssh client")

func ReadText() ([]byte, error)         { return nil, errUnsupported }
func WriteText(_ []byte) error          { return errUnsupported }
func ReadImagePNG() ([]byte, error)     { return nil, errUnsupported }
func WriteImagePNG(_ []byte) error      { return errUnsupported }
