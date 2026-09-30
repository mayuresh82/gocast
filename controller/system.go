package controller

import (
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/golang/glog"

	"github.com/mayuresh82/gocast/controller/iptables"
)

const (
	GocastChain iptables.Chain = "GOCAST"
)

var (
	ipPath string
)

func init() {
	path, err := exec.LookPath("ip")
	if err != nil {
		glog.Exitf("unable to find path for ip: %v\n", err)
		return
	}
	ipPath = path
}

func gateway() (net.IP, error) {
	cmd := exec.Command(ipPath, "route")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %s: %v", cmd, err)
	}
	var line string
	for l := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(l, "default") {
			line = l
			break
		}
	}
	if line == "" {
		return nil, fmt.Errorf("no default gateway found")
	}
	parts := strings.Split(line, " ")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid gateway line: %s", line)
	}
	return net.ParseIP(strings.TrimSpace(parts[2])), nil
}

func via(dest net.IP) (net.IP, error) {
	cmd := exec.Command(ipPath, "route", "get", dest.String())
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %s: %v", cmd, err)
	}
	var line string
	for l := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(l, "via") {
			line = l
			break
		}
	}
	if line == "" {
		// assume the provided dest is the next hop
		return dest, nil
	}
	parts := strings.Split(line, " ")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid via line: %s", line)
	}
	return net.ParseIP(strings.TrimSpace(parts[2])), nil
}

func localAddress(gw net.IP) (net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		switch v := addr.(type) {
		case *net.IPNet:
			if v.Contains(gw) {
				return v.IP, nil
			}
		}
	}
	return nil, fmt.Errorf("unable to find local address")
}

func addLoopback(name string, addr *net.IPNet) error {
	_ = deleteLoopback(addr)
	label := fmt.Sprintf("lo:%s", name)
	// linux kernel limits labels to 15 chars
	if len(label) > 15 {
		label = label[:15]
	}
	cmd := exec.Command(ipPath, "address", "add", addr.String(), "dev", "lo", "label", label)
	glog.V(4).Infof("running command: %s", cmd)

	_, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to add loopback (%s): %v", addr, err)
	}
	return nil
}

func deleteLoopback(addr *net.IPNet) error {
	cmd := exec.Command(ipPath, "address", "delete", addr.String(), "dev", "lo")
	glog.V(4).Infof("running command: %s", cmd)

	_, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to delete loopback (%s): %v", addr, err)
	}
	return nil
}

func natRule(op iptables.Action, vip, localAddr net.IP, protocol, vipPort, localPort string) error {
	var err error
	switch op {
	case iptables.Append:
		err = GocastChain.Add(
			iptables.Nat,
			"-p", protocol,
			"-d", vip.String(),
			"--dport", vipPort,
			"-j", "DNAT",
			"--to-destination", fmt.Sprintf("%s:%s", localAddr.String(), localPort),
		)
	case iptables.Delete:
		err = GocastChain.Remove(
			iptables.Nat,
			"-p", protocol,
			"-d", vip.String(),
			"--dport", vipPort,
			"-j", "DNAT",
			"--to-destination", fmt.Sprintf("%s:%s", localAddr.String(), localPort),
		)
	default:
		err = fmt.Errorf("unknown iptables operation: %s", op)
	}

	if err != nil {
		return fmt.Errorf("failed to update nat rule (%s): %v", op, err)
	}
	return nil
}

func setupChain() (retErr error) {
	err := iptables.AddChain(iptables.Nat, GocastChain)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			if err := iptables.RemoveChain(iptables.Nat, GocastChain); err != nil {
				glog.Error(err)
			}
		}
	}()

	preroute := []string{
		"-m", "addrtype",
		"--dst-type", "LOCAL",
		"-j", string(GocastChain)}
	retErr = iptables.PREROUTING.Add(iptables.Nat, preroute...)
	if retErr != nil {
		return retErr
	}
	defer func() {
		if retErr != nil {
			if err := iptables.PREROUTING.Remove(iptables.Nat, preroute...); err != nil {
				glog.Error(err)
			}
		}
	}()

	output := []string{
		"-m", "addrtype",
		"--dst-type", "LOCAL",
		"-j", string(GocastChain)}
	retErr = iptables.OUTPUT.Add(iptables.Nat, output...)
	if retErr != nil {
		return retErr
	}
	defer func() {
		if retErr != nil {
			if err := iptables.OUTPUT.Remove(iptables.Nat, output...); err != nil {
				glog.Error(err)
			}
		}
	}()

	return nil
}

func teardownChain() error {
	_ = iptables.OUTPUT.Remove(iptables.Nat, "-m", "addrtype", "--dst-type", "LOCAL", "-j", string(GocastChain))
	_ = iptables.PREROUTING.Remove(iptables.Nat, "-m", "addrtype", "--dst-type", "LOCAL", "-j", string(GocastChain))

	_ = iptables.RemoveChain(iptables.Nat, GocastChain)
	return nil
}
