package execuser_test

import (
	userpkg "os/user"
	"strconv"
	"syscall"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/execuser"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	currentUser, err := userpkg.Current()
	require.NoError(t, err)

	currentGroup, err := userpkg.LookupGroupId(currentUser.Gid)
	require.NoError(t, err)

	currentUID, err := strconv.ParseUint(currentUser.Uid, 10, 32)
	require.NoError(t, err)

	currentGID, err := strconv.ParseUint(currentUser.Gid, 10, 32)
	require.NoError(t, err)

	const unregisteredUID = "4294967295"
	_, err = userpkg.LookupId(unregisteredUID)
	require.Error(t, err)

	tests := []struct {
		name string
		spec string
		want *syscall.Credential
	}{
		{
			name: "named user",
			spec: currentUser.Username,
			want: &syscall.Credential{Uid: uint32(currentUID), Gid: uint32(currentGID)},
		},
		{
			name: "numeric user",
			spec: currentUser.Uid,
			want: &syscall.Credential{Uid: uint32(currentUID), Gid: uint32(currentGID)},
		},
		{
			name: "named user and group",
			spec: currentUser.Username + ":" + currentGroup.Name,
			want: &syscall.Credential{Uid: uint32(currentUID), Gid: uint32(currentGID)},
		},
		{
			name: "named user and numeric group",
			spec: currentUser.Username + ":31337",
			want: &syscall.Credential{Uid: uint32(currentUID), Gid: 31337},
		},
		{
			name: "numeric user and numeric group",
			spec: unregisteredUID + ":31337",
			want: &syscall.Credential{Uid: 4294967295, Gid: 31337},
		},
		{
			name: "numeric user and named group",
			spec: unregisteredUID + ":" + currentGroup.Name,
			want: &syscall.Credential{Uid: 4294967295, Gid: uint32(currentGID)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := execuser.Resolve(test.spec)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestResolveRejectsMissingUser(t *testing.T) {
	for _, spec := range []string{"", ":staff"} {
		t.Run(spec, func(t *testing.T) {
			_, err := execuser.Resolve(spec)
			require.ErrorContains(t, err, "invalid user override")
		})
	}
}
