# GoCast

Gocast is a tool that does controller BGP route advertisements from a host. It runs custom defined healthchecks and announces or withdraws routes (most commonly VIPs or Virtual IPs) to a BGP peer.
The most common use case for this is anycast (vip) based load balancing for infrastructure services such as DNS, Syslog etc where several instances are available in geographically diverse regions that announce the same anycast VIP, and clients then get sent to the closest instance.

For some practical examples and more details, check out this blog post : https://mayuresh82.github.io/2020/11/28/automatic_service_discovery_anycast/

# Looking for code reviewers
If you are interested in being a reviewer and/or co-maintainer, please reach out to @mayuresh82 !

## Installation
Use the docker container at mayuresh82/gocast or compile from source:

1. [Install Go](https://golang.org/doc/install)
2. [Set up your GOPATH](https://golang.org/doc/code.html#GOPATH)
3. Run `go get -d github.com/mayuresh82/gocast`
4. Run `cd $GOPATH/src/github.com/mayuresh82/gocast`
5. Run `make`

## Design

GoCast uses [GoBGP](https://github.com/osrg/gobgp) as a library to peer with remote neighbors and announce/withdraw prefixes. It really is just a healthcheck based wrapper around GoBGP. Remote peers can be autodiscovered or statically configured.
A peer will most commonly be a Top-Of-Rack (TOR) switch.

Typically, you would run GoCast on the same hosts as the service that needs to be monitored.
Once an application "registers" with GoCast, GoCast then runs the predefined health monitors/checks and if they fail (e.g a service listening on a specific port), the routes are withdrawn thereby taking the node out of service.

GoCast uses a config file to define agent parameters (http addr, consul server addr, timers, etc.) and BGP parameters (local/peer ASN, peer IP, origin/communnities). See example config.yaml.

### Registration
An application can register with the GoCast instance running on the same host using one of the following methods:
1. http call : Make an http get call with the required parameters. For example:
    ```
    http://gocast-addr/register?name=<appName>&vip=<addr/mask>&monitor=port:tcp:5000
    ```
    Multiple monitors can be defined and the health check succeeds only when all the monitors pass.

2. Custom defined apps in config.yaml. See the example config.yaml for syntax examples
3. Consul based auto-discovery (see below)
4. Nomad based auto-discovery (see below)

## Monitors
A health monitor can either be a port monitor, an exec monitor or consul.
Port monitors are specified as `port:protocol:portnum` , exec monitors run a script or arbitrary command and pass on successful exit (status code 0), specified as `exec:command` and consul monitors use consul's own health checks, specifed simply as `consul`.

## Consul Integration
GoCast supports consul for automatic service discovery and health checking. For this to work, the following needs to be setup:
- The host running GoCast needs to have the environment variable **CONSUL_NODE** set to the hostname in consul

- The following tags must be set in consul for autodiscovery to work:

`enable_gocast` : required

`gocast_vip=<addr/mask>`: required

`gocast_monitor=monitor:params`: optional

If `gocast_monitor=consul` is specified, then GoCast uses the defined health checks in consul as the health monitors for the service.

If `gocast_nat=protocol:listenPort:destinationPort` is specified, then GoCast will create NAT rules, via iptables, and map traffic destined to the assigned VIP and the specified `listenPort` to the physical IP and `destinationPort`.

Example: `gocast_nat=tcp:53:8053` and `gocast_nat=udp:53:8053`

Alternatively, if `gocast_nat=protocol:port` is specified, then GoCast will create NAT rules, via iptables, and map traffic destined to the assigned VIP and the specified `port` to the physical IP and `port`.

Example: `gocast_nat=tcp:53` and `gocast_nat=udp:53`

## Nomad Integration

GoCast supports nomad for automatic service discovery. 
For this to work, it must be configured in the config file and the nomad services need to have a couple tags.

If gocast is running within a nomad allocation, it will default to use the unix socket at `$NOMAD_UNIX_ADDR` instead of the http API.
Nomad calls this socket the "Task API".
This API requires authentication, so either a token will need to be defined in the configuration or the task must have the [workload identity added to the environment](https://developer.hashicorp.com/nomad/docs/concepts/workload-identity#workload-identity-for-nomad):

```hcl
task "example" {
  identity {
    env = true
  }
}
```

### Configuration
```yaml
agent:
  nomad:
    # must be present to enable nomad integration, defaults to false
    enabled: true
    # optional, address where the nomad API can be reached, 
    # defaults to http://127.0.0.1:4646 unless there's an environment 
    # variable called `NOMAD_SECRETS_DIR`
    addr: "http://127.0.0.1:4646"
    # optional, if omitted, doesn't send explicit namespace to nomad 
    # (nomad itself currently defaults to the "default" namespace)
    namespace: "*"
    # optional, if omitted, gocast will query the nomad agent for its node_id
    node_id: ""
    # optional, include if nomad API requires authentication
    token: "token-value"
    # optional, specify if the query interval should be changed,
    # defaults to  30 seconds
    query_interval: 30s
```

## Tags

| Name             | Value                                                 | Required | Documentation                                                                                                                                                                                                                                                                                          |
|------------------|-------------------------------------------------------|----------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enable_gocast`  | N\A                                                   | yes      | Makes the service visible to GoCast                                                                                                                                                                                                                                                                    |
| `gocast_vip`     | `<addr>/<mask>`                                       | yes      | The IP & mask to advertise. e.g. `10.0.0.1/32`                                                                                                                                                                                                                                                         |
| `gocast_monitor` | `port:<protocol>:<port>`<br>`exec:<path>`<br>`consul` | no       | The healthcheck to use to monitor the service. Can be specified multiple times, in which case all healthchecks must pass for the service to be considered "up".                                                                                                                                        |
| `gocast_nat`     | `<protocol>:<listenPort>[:<destinationPort>]`         | no       | Setup an NAT redirection with the given `<protocol>` from `<listenPort>` on the service ip to `<destinationPort>` on the vip. As a shortcut, if the destination port is omitted, the dynamic port will be extracted from the service definition. e.g. `gocast_nat=tcp:53:8053` or `gocast_nat=udp:53`. |

## Docker support
The docker image at mayuresh82/gocast can be used to run GoCast inside a container.
In order for GoCast to manipulate the host network stack correctly, the container needs to run with NET_ADMIN capability and host mode networking. 
For example:
```
docker run -d --cap-add=NET_ADMIN --net=host -v /path/to/host-config:/path/to/container-config mayuresh82/gocast -config=/path/to/config.yaml -logtostderr
```

## Caveats and workarounds

The service to be monitored can also be run inside a container, provided the published service ports are set to listen on 0.0.0.0 (not a specific IP.)
Certain orchestration solutions such as Nomad run the docker containers with published ports listening only on the physical IP address.
If using native nomad service discovery, this is not an issue as the "physical" IP address of the service is extracted and used for the NAT redirection.
However, in other situations (such as consul service discovery or manual (configuration file-based) service discovery), this will cause all requests to the app to fail as the nomad service does not listen on the loopback interface, which is where GoCast assigns the VIP IP.
To work around this there are 2 options:

- Start the service container in host networking mode OR

- Register NAT rules for your service with GoCast for the required protocol/port(s). 
  GoCast will then create iptables NAT rules that map traffic destined to the assigned VIP to the physical IP address.
  This is achieved by adding the `nat=protocol:listenPort:destinationPort` in the http query or `gocast_nat=protocol:listenPort:destinationPort` tag(s) in consul, as shown in the Consul integration section above.

## Why not just use ExaBGP or something similar?

ExaBGP is commonly used for this purpose, with bash scripts and such.
However, I found that there no standard way of doing things and there is little to no support for containerized services.
Also, ExaBGP's API is clunky and documentation is almost nonexistent.
GoCast provides an out-of-the-box solution without hacking together a bunch of scripts.
