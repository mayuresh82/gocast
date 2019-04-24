package controller

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

func gateway(family string) (net.IP, error) {
	ipCmd := "ip"
	if family == "6" {
		ipCmd = "ip -6"
	}
	cmd := fmt.Sprintf(`%s route | grep "^default" | cut -d" " -f3`, ipCmd)
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return nil, fmt.Errorf("Failed to execute command: %s", cmd)
	}
	return net.ParseIP(strings.TrimSpace(string(out))), nil
}

func via(dest net.IP) (string, error) {
	ipCmd := "ip"
	if dest.To4() == nil {
		ipCmd = "ip -6"
	}
	cmd := fmt.Sprintf(`%s route get %s | grep src | cut -d" " -f3`, ipCmd, dest.String())
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return "", fmt.Errorf("Failed to execute command: %s", cmd)
	}
	return strings.TrimSpace(string(out)), nil
}

func localAddress(dev string, family string) (net.IP, error) {
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return nil, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if family == "4" && ip.To4() != nil {
			return ip, nil
		}
		if family == "6" && ip.To4() == nil && !ip.IsLinkLocalUnicast() {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("Unable to find local address")
}

func addLoopback(name string, vip *Vip) error {
	deleteLoopback(vip)
	prefixLen, _ := vip.Net.Mask.Size()
	label := fmt.Sprintf("lo:%s", name)
	// linux kernel limits labels to 15 chars
	if len(label) > 15 {
		label = label[:15]
	}
	ipCmd := "ip"
	if vip.Family == "6" {
		ipCmd = "ip -6"
	}
	cmd := fmt.Sprintf("%s address add %s/%d dev lo label %s", ipCmd, vip.IP.String(), prefixLen, label)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to Add loopback command: %s: %v", cmd, err)
	}
	return nil
}

func deleteLoopback(vip *Vip) error {
	prefixLen, _ := vip.Net.Mask.Size()
	ipCmd := "ip"
	if vip.Family == "6" {
		ipCmd = "ip -6"
	}
	cmd := fmt.Sprintf("%s address delete %s/%d dev lo", ipCmd, vip.IP.String(), prefixLen)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to delete loopback command: %s: %v", cmd, err)
	}
	return nil
}

func natRule(op string, vip, localAddr net.IP, protocol, port string) error {
	iptCmd := "iptables"
	toDest := fmt.Sprintf("%s:%s", localAddr.String(), port)
	if vip.To4() == nil {
		iptCmd = "ip6tables"
		toDest = fmt.Sprintf("[%s]:%s", localAddr.String(), port)
	}
	cmd := fmt.Sprintf(
		"%s -t nat -%s PREROUTING -p %s -d %s --dport %s -j DNAT --to-destination %s",
		iptCmd, op, protocol, vip.String(), port, toDest,
	)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to %s nat rule: %s: %v", op, cmd, err)
	}
	return nil
}
