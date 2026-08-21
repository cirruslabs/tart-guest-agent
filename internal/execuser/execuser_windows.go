//go:build windows

package execuser

import "fmt"

func Resolve(spec string) (any, error) {
	if spec == "" {
		return nil, nil
	}
	return nil, fmt.Errorf("user switching is not supported on Windows")
}
