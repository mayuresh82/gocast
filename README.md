# GoCast

Gocast is a tool that does controller BGP route advertisements from a host. It runs custom defined healthchecks and announces or withdraws routes (most commonly VIPs or Virtual IPs) to a BGP peer.
The most common use case for this is anycast (vip) based load balancing for infrastructure services such as DNS, Syslog etc where several instances are available in geographically diverse regions that announce the same anycast VIP, and clients then get sent to the closest instance.

For some practical examples and more details, check out this blog post : https://mayuresh82.github.io/2020/11/28/automatic_service_discovery_anycast/

# Looking for code reviewers
If you are interested in being a reviewer and/or co-maintainer, please reach out to @mayuresh82 !

## Installation
Use the docker container at mayuresh82/gocast or compile from source:

1. [Install Go](https://golang.org/doc/install)
2. [Setup your GOPATH](https://golang.org/doc/code.html#GOPATH)
3. Run `go get -d github.com/mayuresh82/gocast`
4. Run `cd $GOPATH/src/github.com/mayuresh82/gocast`
5. Run `make`

## Design

GoCast uses [GoBGP](https://github.com/osrg/gobgp) as a library to peer with remote neighbors and announce/withdraw prefixes. It really is just a healthcheck based wrapper around GoBGP. Remote peers can be autodiscovered or statically configured. A peer will most commonly be a Top-Of-Rack (TOR) switch.

Typically you would run GoCast on the same hosts as the service that needs to be monitored.
Once an application "registers" with GoCast, GoCast then runs the predefined health monitors/checks and if they fail (e.g a service listening on a specific port), the routes are withdrawn thereby taking the node out of service.

GoCast uses a config file to define agent parameters (http addr, consul server addr, timers etc) and BGP parameters (local/peer ASN, peer IP, origin/communnities). See example config.yaml.

### Registration
An application can register with the GoCast instance running on the same host using one of the following methods:
1. http call : Make an http get call with the required parameters. For example:
```
http://gocast-addr/register?name=<appName>&vip=<addr/mask>&monitor=port:tcp:5000
```
Multiple monitors can be defined and the healthcheck succeeds only when all the monitors pass.

2. Custom defined apps in config.yaml. See the example config.yaml for syntax examples

3. Consul based auto-discovery (see below)

## Monitors
A health monitor can either be a port monitor, an exec monitor or consul. Port monitors are specified as *port:protocol:portnum* , exec monitors run a script or arbitrary command and pass on successful exit (status code 0), specified as *exec:command* and consul monitors use consul's own healthchecks, specifed simply as *consul*

## Consul Integration
GoCast supports consul for automatic service discovery and healthchecking. For this to work, the following needs to be setup:
- The host running GoCast needs to have the environment variable **CONSUL_NODE** set to the hostname in consul

- The following tags must be set in consul for autodiscovery to work:

`enable_gocast` : required

`gocast_vip=<addr/mask>`: required

`gocast_monitor=monitor:params`: optional

If `gocast_monitor=consul` is specified, then GoCast uses the defined healthchecks in consul as the health monitors for the service.

If `gocast_nat=protocol:listenPort:destinationPort` is specified, then GoCast will create NAT rules, via iptables, and map traffic destined to the assigned VIP and the specified `listenPort` to the physical IP and `destinationPort`.

Example: `gocast_nat=tcp:53:8053` and `gocast_nat=udp:53:8053`

Alternatively, if `gocast_nat=protocol:port` is specified, then GoCast will create NAT rules, via iptables, and map traffic destined to the assigned VIP and the specified `port` to the physical IP and `port`.

Example: `gocast_nat=tcp:53` and `gocast_nat=udp:53`

## Docker support
The docker image at mayuresh82/gocast can be used to run GoCast inside a container. In order for GoCast to manipulate the host network stack correctly, the container needs to run with NET_ADMIN capablity and host mode networking. For example:
```
docker run -d --cap-add=NET_ADMIN --net=host -v /path/to/host-config:/path/to/container-config mayuresh82/gocast -config=/path/to/config.yaml -logtostderr
```

**Caveats and workarounds**

The service to be monitored can also be run inside a container, provided the published service ports are set to listen on 0.0.0.0 (not a specific IP.)
Certain orchestration solutions such as Nomad run the docker containers with published ports listening only on the physical IP address. This will cause all requests to the app to fail, because the host does not listen on the loopback interface any more (which GoCast uses and assigns the VIP IP to). To work=around this there are 2 options:

- Start the service container in host networking mode OR

- Register NAT rules for your service with GoCast for the required protocol/port(s). GoCast will then create iptables NAT rules that map traffic destined to the assigned VIP to the physical IP address. This is achieved by adding the `nat=protocol:listenPort:destinationPort` in the http query or `gocast_nat=protocol:listenPort:destinationPort` tag(s) in consul, as shown in the Consul integration section above.

**Why not just use ExaBGP or something similar ?**

ExaBGP is commonly used for this purpose, with bash scripts and such.  However, I found that there no standard way of doing things and there is little to no support for containerized services. Also ExaBGP's API is clunky and documentation is almost non existent. GoCast provides an out of the box solution without hacking together a bunch of scripts.
