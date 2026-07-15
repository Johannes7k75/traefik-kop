package traefikkop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
}

type fakeDockerClient struct {
	client.APIClient
	containers []container.Summary
	container  container.InspectResponse
	err        error
}

func (c *fakeDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	return c.containers, nil
}

func (c *fakeDockerClient) ContainerInspect(ctx context.Context, container string) (container.InspectResponse, error) {
	return c.container, c.err
}

func Test_replaceIPs(t *testing.T) {
	cfg := &dynamic.Configuration{}
	err := json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)
	require.Contains(t, cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2")

	fc := &dockerCache{client: &fakeDockerClient{}, list: nil, details: make(map[string]container.InspectResponse)}

	// replace and test check again
	replaceIPs(fc, cfg, "7.7.7.7", false)
	require.NotContains(t, cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2")

	// full url
	require.Equal(t, "http://7.7.7.7:80", cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL)

	// test again with larger fixture, tcp service
	cfg = &dynamic.Configuration{}
	_, err = toml.DecodeFile("./fixtures/sample.toml", &cfg)
	require.NoError(t, err)
	require.Equal(t, "foobar", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
	replaceIPs(fc, cfg, "7.7.7.7", false)
	require.Equal(t, "7.7.7.7", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
}

func createTestClient(labels map[string]string) *fakeDockerClient {
	return &fakeDockerClient{
		containers: []container.Summary{
			container.Summary{
				ID: "foobar_id",
			},
		},
		container: container.InspectResponse{
			ContainerJSONBase: &types.ContainerJSONBase{
				ID:         "foobar_id",
				HostConfig: &container.HostConfig{},
			},
			Config: &container.Config{
				Labels: labels,
			},
		},
	}

}

func Test_replacePorts(t *testing.T) {
	log.Debug().Msg("Testing replacePorts")

	portLabel := "traefik.http.services.nginx.loadbalancer.server.port"
	dc := createTestClient(map[string]string{
		"traefik.http.services.nginx.loadbalancer.server.scheme": "http",
		portLabel: "8888",
	})

	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}

	cfg := &dynamic.Configuration{}
	err := json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2:80"))

	// explicit label present
	log.Debug().Msg("explicit label present")
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"), "URL '%s' should end with '%s'", cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888")

	// without label but no port binding
	log.Debug().Msg("without label but no port binding")
	delete(dc.container.Config.Labels, portLabel)
	json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))

	// with port binding
	log.Debug().Msg("with port binding")
	portMap := nat.PortMap{
		"80": []nat.PortBinding{
			{HostIP: "172.20.0.2", HostPort: "8888"},
		},
	}

	dc.container.HostConfig.PortBindings = portMap
	logJSON("container", dc.container)
	json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.False(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"))
}

func Test_replacePortsNoService(t *testing.T) {

	portMap := nat.PortMap{
		"80": []nat.PortBinding{
			{HostIP: "172.20.0.2", HostPort: "8888"},
		},
	}

	dc := createTestClient(map[string]string{
		"traefik.http.routers.nginx.entrypoints": "web-secure",
	})
	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}

	cfg := &dynamic.Configuration{}
	err := json.Unmarshal([]byte(NGINX_CONF_JSON_DIFFRENT_SERVICE_NAME), cfg)
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2:80"))

	// explicit label present
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))

	// without label but no port binding
	json.Unmarshal([]byte(NGINX_CONF_JSON_DIFFRENT_SERVICE_NAME), cfg)
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))

	// with port binding
	dc.container.HostConfig.PortBindings = portMap
	json.Unmarshal([]byte(NGINX_CONF_JSON_DIFFRENT_SERVICE_NAME), cfg)
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.False(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"))
}

func Test_resolveInternalPortsHTTP(t *testing.T) {
	portMap := nat.PortMap{
		"80/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "8888"}},
	}

	dc := createTestClient(map[string]string{
		"traefik.http.routers.nginx.entrypoints": "web-secure",
	})
	dc.container.HostConfig.PortBindings = portMap

	dc.container.NetworkSettings = &container.NetworkSettings{}
	dc.container.NetworkSettings.Ports = portMap

	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}

	cfg := &dynamic.Configuration{}
	require.NoError(t, json.Unmarshal([]byte(NGINX_CONF_JSON), cfg))

	// global flag false => resolve to host port 8888 because only one port exposed
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"))

	// global flag true => resolve to host port 8888
	json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	replaceIPs(fc, cfg, "4.4.4.4", true)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"))
}

func Test_resolveInternalPortsLabelOverride(t *testing.T) {
	portMap := nat.PortMap{
		"80/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "8888"}},
		"81/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "8181"}},
	}

	// global false, per-service label true
	dc := createTestClient(map[string]string{
		"traefik.http.routers.nginx.entrypoints":               "web-secure",
		"traefik.http.services.nginx.loadbalancer.server.port": "80",
		"kop.http.services.nginx.resolve-internal-ports":       "true",
	})
	dc.container.HostConfig.PortBindings = portMap

	dc.container.NetworkSettings = &container.NetworkSettings{}
	dc.container.NetworkSettings.Ports = portMap

	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}
	cfg := &dynamic.Configuration{}
	require.NoError(t, json.Unmarshal([]byte(NGINX_CONF_JSON), cfg))

	replaceIPs(fc, cfg, "4.4.4.4", false)
	log.Debug().Msgf("cfg: %v", cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"))

	// global true, per-service label false
	dc.container.Config.Labels = map[string]string{
		"kop.http.services.nginx.resolve-internal-ports": "false",
	}
	json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	replaceIPs(fc, cfg, "4.4.4.4", true)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))
}

func Test_resolvePortLabel(t *testing.T) {
	portMap := nat.PortMap{
		"80/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "8888"}},
		"90/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "9000"}},
	}

	dc := createTestClient(map[string]string{
		"traefik.http.routers.nginx.entrypoints": "web-secure",
		"kop.http.services.nginx.resolve-port":   "90",
	})
	dc.container.HostConfig.PortBindings = portMap

	dc.container.NetworkSettings = &container.NetworkSettings{}
	dc.container.NetworkSettings.Ports = portMap

	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}
	cfg := &dynamic.Configuration{}
	require.NoError(t, json.Unmarshal([]byte(NGINX_CONF_JSON), cfg))

	// resolve-port label should override and return host port 9000
	replaceIPs(fc, cfg, "4.4.4.4", false)
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:9000"))
}

func Test_resolveInternalPortsTCP(t *testing.T) {
	portMap := nat.PortMap{
		"80/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "8888"}},
		"10/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "10000"}},
	}

	dc := createTestClient(map[string]string{
		"traefik.tcp.routers.nginx-tcp.entrypoints":               "web",
		"traefik.tcp.services.nginx-tcp.loadbalancer.server.port": "80",
	})
	dc.container.HostConfig.PortBindings = portMap

	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}

	cfg := &dynamic.Configuration{}

	err := json.Unmarshal([]byte(NGINX_CONF_JSON_TCP), cfg)
	require.NoError(t, err)

	replaceIPs(fc, cfg, "4.4.4.4", true)
	require.Equal(t, "4.4.4.4:8888", cfg.TCP.Services["nginx-tcp@docker"].LoadBalancer.Servers[0].Address)
}

func Test_resolveInternalPortsUDP(t *testing.T) {
	portMap := nat.PortMap{
		"90/udp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "9000"}},
		"10/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "10000"}},
	}

	dc := createTestClient(map[string]string{
		"traefik.udp.routers.nginx-udp.entrypoints":               "udp",
		"traefik.udp.services.nginx-udp.loadbalancer.server.port": "90",
	})
	dc.container.HostConfig.PortBindings = portMap

	dc.container.NetworkSettings = &container.NetworkSettings{}
	dc.container.NetworkSettings.Ports = portMap

	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}

	cfg := &dynamic.Configuration{}

	err := json.Unmarshal([]byte(NGINX_CONF_JSON_UDP), cfg)
	require.NoError(t, err)

	replaceIPs(fc, cfg, "4.4.4.4", true)
	require.Equal(t, "4.4.4.4:9000", cfg.UDP.Services["nginx-udp@docker"].LoadBalancer.Servers[0].Address)
}

func Test_resolveInternalPortsProtocolFiltering(t *testing.T) {
	// TCP 22 -> 2222 and UDP 90 -> 9000 on the same container
	portMap := nat.PortMap{
		"22/tcp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "2222"}},
		"90/udp": []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "9000"}},
	}

	dc := createTestClient(map[string]string{
		"traefik.tcp.routers.ssh.entrypoints": "ssh",
		// "traefik.tcp.services.ssh.loadbalancer.server.port": "22",
		"traefik.udp.routers.udp.entrypoints": "udp",
		// "traefik.udp.services.udp.loadbalancer.server.port": "90",
	})
	dc.container.HostConfig.PortBindings = portMap

	dc.container.NetworkSettings = &container.NetworkSettings{}
	dc.container.NetworkSettings.Ports = portMap

	fc := &dockerCache{client: dc, list: nil, details: make(map[string]container.InspectResponse)}

	cfg := &dynamic.Configuration{
		TCP: &dynamic.TCPConfiguration{
			Routers: map[string]*dynamic.TCPRouter{
				"ssh@docker": {Service: "ssh"},
			},
			Services: map[string]*dynamic.TCPService{
				"ssh@docker": {
					LoadBalancer: &dynamic.TCPServersLoadBalancer{
						Servers: []dynamic.TCPServer{
							{Address: "172.20.0.2:22"},
						},
					},
				},
			},
		},
		UDP: &dynamic.UDPConfiguration{
			Routers: map[string]*dynamic.UDPRouter{
				"udp@docker": {Service: "udp"},
			},
			Services: map[string]*dynamic.UDPService{
				"udp@docker": {
					LoadBalancer: &dynamic.UDPServersLoadBalancer{
						Servers: []dynamic.UDPServer{
							{Address: "172.20.0.2:90"},
						},
					},
				},
			},
		},
	}

	replaceIPs(fc, cfg, "4.4.4.4", true)
	require.Equal(t, "4.4.4.4:2222", cfg.TCP.Services["ssh@docker"].LoadBalancer.Servers[0].Address)
	require.Equal(t, "4.4.4.4:9000", cfg.UDP.Services["udp@docker"].LoadBalancer.Servers[0].Address)
}
