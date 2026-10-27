//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startAdminer runs Adminer, a web UI for both MySQL and Postgres, and returns
// pre-filled login links for MySQL and for each named Postgres database.
// Adminer reaches them through the host's mapped ports via host.docker.internal.
func startAdminer(ctx context.Context, my, pg testcontainers.Container, pgDBs ...string) (c testcontainers.Container, mysqlLink string, pgLinks []string, err error) {
	c, err = testcontainers.Run(ctx, "adminer:4.8.1",
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			// Docker Desktop provides this name already; Linux needs the mapping.
			hc.ExtraHosts = append(hc.ExtraHosts, "host.docker.internal:host-gateway")
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/").WithPort("8080/tcp")),
	)
	if err != nil {
		return c, "", nil, err
	}
	base, err := c.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		return c, "", nil, err
	}
	myPort, err := hostPort(ctx, my, "3306/tcp")
	if err != nil {
		return c, "", nil, err
	}
	pgPort, err := hostPort(ctx, pg, "5432/tcp")
	if err != nil {
		return c, "", nil, err
	}
	mysqlLink = base + "/?" + url.Values{
		"server": {"host.docker.internal:" + myPort}, "username": {"buisy"}, "db": {"burnmfgsystems_db"},
	}.Encode()
	for _, db := range pgDBs {
		pgLinks = append(pgLinks, base+"/?"+url.Values{
			"pgsql": {"host.docker.internal:" + pgPort}, "username": {"nexus"}, "db": {db}, "ns": {"buisy_nexus_staging"},
		}.Encode())
	}
	return c, mysqlLink, pgLinks, nil
}

func hostPort(ctx context.Context, c testcontainers.Container, port string) (string, error) {
	ep, err := c.PortEndpoint(ctx, port, "")
	if err != nil {
		return "", err
	}
	i := strings.LastIndex(ep, ":")
	if i < 0 {
		return "", fmt.Errorf("unexpected endpoint %q", ep)
	}
	return ep[i+1:], nil
}
