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

	// Resolve the user by numeric ID or name
	var user *userpkg.User
	var err error

	if _, err = strconv.ParseUint(userPart, 10, 32); err == nil {
		user, err = userpkg.LookupId(userPart)
	} else {
		user, err = userpkg.Lookup(userPart)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to resolve user %q: %w", userPart, err)
	}

	// User resolution yields strings, so we need to parse them first
	uid, err := strconv.ParseUint(user.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse UID %q: %w", user.Uid, err)
	}

	gid, err := strconv.ParseUint(user.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GID %q: %w", user.Gid, err)
	}

	// Keep the user's primary GID when no group override is provided
	if groupPart == "" {
		return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, nil
	}

	// When group override is provided, and it's numeric, use it directly
	if groupGID, err := strconv.ParseUint(groupPart, 10, 32); err == nil {
		return &syscall.Credential{Uid: uint32(uid), Gid: uint32(groupGID)}, nil
	}

	// Otherwise, resolve named group override through the system user database
	group, err := userpkg.LookupGroup(groupPart)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve group %q: %w", groupPart, err)
	}

	groupGID, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GID %q: %w", group.Gid, err)
	}

	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(groupGID)}, nil
}
