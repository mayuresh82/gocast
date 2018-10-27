package controller

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

func gateway() (net.IP, error) {
	cmd := `ip route | grep "^default" | cut -d" " -f3`
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return nil, fmt.Errorf("Failed to execute command: %s", cmd)
	}
	return net.ParseIP(strings.TrimSpace(string(out))), nil
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
	return nil, fmt.Errorf("Unable to find local address")
}

func addLoopback(name string, addr *net.IPNet) error {
	deleteLoopback(addr)
	prefixLen, _ := addr.Mask.Size()
	label := fmt.Sprintf("lo:%s", name)
	// linux kernel limits labels to 15 chars
	if len(label) > 15 {
		label = label[:15]
	}
	cmd := fmt.Sprintf("ip address add %s/%d dev lo label %s", addr.IP.String(), prefixLen, label)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to Add loopback command: %s: %v", cmd, err)
	}
	return nil
}

func deleteLoopback(addr *net.IPNet) error {
	prefixLen, _ := addr.Mask.Size()
	cmd := fmt.Sprintf("ip address delete %s/%d dev lo", addr.IP.String(), prefixLen)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to delete loopback command: %s: %v", cmd, err)
	}
	return nil
}

func natRule(op string, vip, localAddr net.IP, protocol, port string) error {
	cmd := fmt.Sprintf(
		"iptables -t nat -%s PREROUTING -p %s -d %s --dport %s -j DNAT --to-destination %s:%s",
		op, protocol, vip.String(), port, localAddr.String(), port,
	)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to %s nat rule: %s: %v", op, cmd, err)
	}
	return nil
}
