// Copyright (c) 2020 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package controlclient

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"inet.af/netaddr"
	"tailscale.com/tailcfg"
	"tailscale.com/types/wgkey"
)

func TestUndeltaPeers(t *testing.T) {
	defer func(old func() time.Time) { clockNow = old }(clockNow)

	var curTime time.Time
	clockNow = func() time.Time {
		return curTime
	}
	online := func(v bool) func(*tailcfg.Node) {
		return func(n *tailcfg.Node) {
			n.Online = &v
		}
	}
	seenAt := func(t time.Time) func(*tailcfg.Node) {
		return func(n *tailcfg.Node) {
			n.LastSeen = &t
		}
	}
	n := func(id tailcfg.NodeID, name string, mod ...func(*tailcfg.Node)) *tailcfg.Node {
		n := &tailcfg.Node{ID: id, Name: name}
		for _, f := range mod {
			f(n)
		}
		return n
	}
	peers := func(nv ...*tailcfg.Node) []*tailcfg.Node { return nv }
	tests := []struct {
		name    string
		mapRes  *tailcfg.MapResponse
		curTime time.Time
		prev    []*tailcfg.Node
		want    []*tailcfg.Node
	}{
		{
			name: "full_peers",
			mapRes: &tailcfg.MapResponse{
				Peers: peers(n(1, "foo"), n(2, "bar")),
			},
			want: peers(n(1, "foo"), n(2, "bar")),
		},
		{
			name: "full_peers_ignores_deltas",
			mapRes: &tailcfg.MapResponse{
				Peers:        peers(n(1, "foo"), n(2, "bar")),
				PeersRemoved: []tailcfg.NodeID{2},
			},
			want: peers(n(1, "foo"), n(2, "bar")),
		},
		{
			name: "add_and_update",
			prev: peers(n(1, "foo"), n(2, "bar")),
			mapRes: &tailcfg.MapResponse{
				PeersChanged: peers(n(0, "zero"), n(2, "bar2"), n(3, "three")),
			},
			want: peers(n(0, "zero"), n(1, "foo"), n(2, "bar2"), n(3, "three")),
		},
		{
			name: "remove",
			prev: peers(n(1, "foo"), n(2, "bar")),
			mapRes: &tailcfg.MapResponse{
				PeersRemoved: []tailcfg.NodeID{1},
			},
			want: peers(n(2, "bar")),
		},
		{
			name: "add_and_remove",
			prev: peers(n(1, "foo"), n(2, "bar")),
			mapRes: &tailcfg.MapResponse{
				PeersChanged: peers(n(1, "foo2")),
				PeersRemoved: []tailcfg.NodeID{2},
			},
			want: peers(n(1, "foo2")),
		},
		{
			name:   "unchanged",
			prev:   peers(n(1, "foo"), n(2, "bar")),
			mapRes: &tailcfg.MapResponse{},
			want:   peers(n(1, "foo"), n(2, "bar")),
		},
		{
			name: "online_change",
			prev: peers(n(1, "foo"), n(2, "bar")),
			mapRes: &tailcfg.MapResponse{
				OnlineChange: map[tailcfg.NodeID]bool{
					1: true,
				},
			},
			want: peers(
				n(1, "foo", online(true)),
				n(2, "bar"),
			),
		},
		{
			name: "online_change_offline",
			prev: peers(n(1, "foo"), n(2, "bar")),
			mapRes: &tailcfg.MapResponse{
				OnlineChange: map[tailcfg.NodeID]bool{
					1: false,
					2: true,
				},
			},
			want: peers(
				n(1, "foo", online(false)),
				n(2, "bar", online(true)),
			),
		},
		{
			name:    "peer_seen_at",
			prev:    peers(n(1, "foo", seenAt(time.Unix(111, 0))), n(2, "bar")),
			curTime: time.Unix(123, 0),
			mapRes: &tailcfg.MapResponse{
				PeerSeenChange: map[tailcfg.NodeID]bool{
					1: false,
					2: true,
				},
			},
			want: peers(
				n(1, "foo"),
				n(2, "bar", seenAt(time.Unix(123, 0))),
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.curTime.IsZero() {
				curTime = tt.curTime
			}
			undeltaPeers(tt.mapRes, tt.prev)
			if !reflect.DeepEqual(tt.mapRes.Peers, tt.want) {
				t.Errorf("wrong results\n got: %s\nwant: %s", formatNodes(tt.mapRes.Peers), formatNodes(tt.want))
			}
		})
	}
}

func formatNodes(nodes []*tailcfg.Node) string {
	var sb strings.Builder
	for i, n := range nodes {
		if i > 0 {
			sb.WriteString(", ")
		}
		var extra string
		if n.Online != nil {
			extra += fmt.Sprintf(", online=%v", *n.Online)
		}
		if n.LastSeen != nil {
			extra += fmt.Sprintf(", lastSeen=%v", n.LastSeen.Unix())
		}
		fmt.Fprintf(&sb, "(%d, %q%s)", n.ID, n.Name, extra)
	}
	return sb.String()
}

func TestNewDirect(t *testing.T) {
	hi := NewHostinfo()
	ni := tailcfg.NetInfo{LinkType: "wired"}
	hi.NetInfo = &ni

	key, err := wgkey.NewPrivate()
	if err != nil {
		t.Error(err)
	}
	opts := Options{
		ServerURL: "https://example.com",
		Hostinfo:  hi,
		GetMachinePrivateKey: func() (wgkey.Private, error) {
			return key, nil
		},
	}
	c, err := NewDirect(opts)
	if err != nil {
		t.Fatal(err)
	}

	if c.serverURL != opts.ServerURL {
		t.Errorf("c.serverURL got %v want %v", c.serverURL, opts.ServerURL)
	}

	if !hi.Equal(c.hostinfo) {
		t.Errorf("c.hostinfo got %v want %v", c.hostinfo, hi)
	}

	changed := c.SetNetInfo(&ni)
	if changed {
		t.Errorf("c.SetNetInfo(ni) want false got %v", changed)
	}
	ni = tailcfg.NetInfo{LinkType: "wifi"}
	changed = c.SetNetInfo(&ni)
	if !changed {
		t.Errorf("c.SetNetInfo(ni) want true got %v", changed)
	}

	changed = c.SetHostinfo(hi)
	if changed {
		t.Errorf("c.SetHostinfo(hi) want false got %v", changed)
	}
	hi = NewHostinfo()
	hi.Hostname = "different host name"
	changed = c.SetHostinfo(hi)
	if !changed {
		t.Errorf("c.SetHostinfo(hi) want true got %v", changed)
	}

	endpoints := fakeEndpoints(1, 2, 3)
	changed = c.newEndpoints(12, endpoints)
	if !changed {
		t.Errorf("c.newEndpoints(12) want true got %v", changed)
	}
	changed = c.newEndpoints(12, endpoints)
	if changed {
		t.Errorf("c.newEndpoints(12) want false got %v", changed)
	}
	changed = c.newEndpoints(13, endpoints)
	if !changed {
		t.Errorf("c.newEndpoints(13) want true got %v", changed)
	}
	endpoints = fakeEndpoints(4, 5, 6)
	changed = c.newEndpoints(13, endpoints)
	if !changed {
		t.Errorf("c.newEndpoints(13) want true got %v", changed)
	}
}

func fakeEndpoints(ports ...uint16) (ret []tailcfg.Endpoint) {
	for _, port := range ports {
		ret = append(ret, tailcfg.Endpoint{
			Addr: netaddr.IPPort{Port: port},
		})
	}
	return
}

func TestNewHostinfo(t *testing.T) {
	hi := NewHostinfo()
	if hi == nil {
		t.Fatal("no Hostinfo")
	}
	j, err := json.MarshalIndent(hi, "  ", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Got: %s", j)
}
