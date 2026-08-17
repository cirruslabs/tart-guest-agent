// Package execuser resolves user overrides for guest exec processes.
package execuser

import (
	"fmt"
	userpkg "os/user"
	"strconv"
	"strings"
	"syscall"
)

// Resolve parses a user override in user[:group] form.
//
// User and group components may be names or numeric IDs.
func Resolve(spec string) (*syscall.Credential, error) {
	// Split the override into user and optional group
	userPart, groupPart, _ := strings.Cut(spec, ":")
	if userPart == "" {
		return nil, fmt.Errorf("invalid user override %q", spec)
	}

	var user *userpkg.User

	uid, err := strconv.ParseUint(userPart, 10, 32)
	if err != nil {
		// Resolve named user
		user, err = userpkg.Lookup(userPart)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve user %q: %w", userPart, err)
		}

		uid, err = strconv.ParseUint(user.Uid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("failed to parse UID %q: %w", user.Uid, err)
		}
	} else if groupPart == "" {
		// Resolve numeric user to obtain their primary group
		user, err = userpkg.LookupId(userPart)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve user %q: %w", userPart, err)
		}
	}

	gid, err := strconv.ParseUint(groupPart, 10, 32)
	if err != nil {
		if groupPart == "" {
			// Use user's primary group
			gid, err = strconv.ParseUint(user.Gid, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("failed to parse GID %q: %w", user.Gid, err)
			}
		} else {
			// Resolve named group
			group, err := userpkg.LookupGroup(groupPart)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve group %q: %w", groupPart, err)
			}

			gid, err = strconv.ParseUint(group.Gid, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("failed to parse GID %q: %w", group.Gid, err)
			}
		}
	}

	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, nil
}
