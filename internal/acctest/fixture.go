// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package acctest provides shared helpers for the provider's acceptance
// tests: a per-test Garage container fixture and the framework
// boilerplate every Test* function needs (PreCheck, provider factories,
// HCL config helpers).
//
// See ADR-0005 for the why behind testcontainers-go + per-test
// containers.
package acctest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// garageImage is pinned per ADR-0005 to v2.3.0 — the release that
// introduced `--single-node --default-bucket`, collapsing the multi-step
// bootstrap into a single-process startup. Bumps are deliberate edits.
const garageImage = "dxflrs/garage:v2.3.0"

// adminPort is the TCP port Garage's admin API listens on inside the
// container. Mapped to a random host port at runtime.
const adminPort = "3903/tcp"

// startupTimeout bounds how long the fixture waits for Garage's admin
// endpoint to become responsive. Cold container starts on macOS Docker
// Desktop are routinely 3-5s; 30s leaves headroom for slow CI runners.
const startupTimeout = 30 * time.Second

// minimumConfigTOML is the smallest config.toml Garage v2.3.0 needs to
// boot in single-node mode. Secrets (rpc_secret) are randomized at
// container start by the fixture — never reused across test runs.
//
// The %s placeholder accepts a per-fixture random rpc_secret hex string.
const minimumConfigTOML = `
metadata_dir = "/var/lib/garage/meta"
data_dir = "/var/lib/garage/data"
db_engine = "sqlite"

replication_factor = 1

rpc_bind_addr = "[::]:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "%s"

[s3_api]
api_bind_addr = "[::]:3900"
s3_region = "garage"
root_domain = ".s3.garage.local"

[admin]
api_bind_addr = "[::]:3903"
`

// Garage is a running Garage admin v2 instance suitable for one
// acceptance test. Endpoint and AdminToken are stable for the lifetime
// of the container; cleanup is registered via t.Cleanup so callers
// don't have to.
type Garage struct {
	container  testcontainers.Container
	Endpoint   string
	AdminToken string
}

// Start launches a fresh Garage container, waits for the admin endpoint
// to become reachable, and returns a fully-populated *Garage. Calls
// t.Fatal on any failure path so callers can treat the return value as
// always-non-nil.
//
// Per ADR-0005, every Test* function gets its own container. Cold-start
// cost (~2-5s) buys full state isolation between tests.
func Start(t *testing.T) *Garage {
	t.Helper()

	ctx := context.Background()
	adminToken := randomHex(t, 32)
	rpcSecret := randomHex(t, 32)
	accessKey := "GK" + randomHex(t, 12)
	secretKey := randomHex(t, 32)
	bucketName := "test-" + randomHex(t, 6)

	configTOML := fmt.Sprintf(minimumConfigTOML, rpcSecret)

	req := testcontainers.ContainerRequest{
		Image:        garageImage,
		ExposedPorts: []string{adminPort},
		Cmd:          []string{"server", "--single-node", "--default-bucket"},
		Env: map[string]string{
			"GARAGE_ADMIN_TOKEN":        adminToken,
			"GARAGE_DEFAULT_ACCESS_KEY": accessKey,
			"GARAGE_DEFAULT_SECRET_KEY": secretKey,
			"GARAGE_DEFAULT_BUCKET":     bucketName,
		},
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader(configTOML),
				ContainerFilePath: "/etc/garage.toml",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForHTTP("/v2/GetClusterStatus").
			WithPort(adminPort).
			WithHeaders(map[string]string{"Authorization": "Bearer " + adminToken}).
			WithStartupTimeout(startupTimeout),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("acctest: start garage container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("acctest: terminate garage container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("acctest: resolve host: %v", err)
	}
	mapped, err := container.MappedPort(ctx, adminPort)
	if err != nil {
		t.Fatalf("acctest: resolve mapped admin port: %v", err)
	}

	return &Garage{
		container:  container,
		Endpoint:   fmt.Sprintf("http://%s:%s", host, mapped.Port()),
		AdminToken: adminToken,
	}
}

// randomHex returns a hex-encoded string of `n` random bytes. Fails the
// test if the system RNG misbehaves, which on a healthy system never
// happens.
func randomHex(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("acctest: read random bytes: %v", err)
	}
	return hex.EncodeToString(buf)
}
