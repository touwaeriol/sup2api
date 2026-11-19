package pluginsdk_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// GetUsers asks the host at most protocol.GetUsersMaxIDs ids at a time and
// answers each user once, in the order asked.
func TestGetUsersBatches(t *testing.T) {
	fh := pluginsdktest.NewFakeHost()
	fh.Users = map[int64]*pluginv1.UserInfo{}
	ids := []int64{}
	for id := int64(1); id <= protocol.GetUsersMaxIDs+2; id++ {
		fh.Users[id] = &pluginv1.UserInfo{Id: id}
		ids = append(ids, id)
	}
	ids = append(ids, 1, 999999)
	p := &locker{}
	pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, Manifest: []byte(lockManifest)})

	users, err := pluginsdk.GetUsers(context.Background(), p.host, ids)
	require.NoError(t, err)
	require.Len(t, users, protocol.GetUsersMaxIDs+2)
	for i, u := range users {
		require.Equal(t, int64(i+1), u.GetId())
	}
}
