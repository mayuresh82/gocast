package controller

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

var execCmd = "bash"

func getCmdList(mainCmd string) []string {
	cmdList := []string{}
	if execCmd == "bash" {
		cmdList = append(cmdList, "-c")
	}
	cmdList = append(cmdList, mainCmd)
	return cmdList
}

func gateway(family int) (net.IP, error) {
	prefix := "ip"
	if family == 6 {
		prefix = "ip -6"
	}
	cmd := fmt.Sprintf(`%s route | grep "^default" | cut -d" " -f3`, prefix)
	cmdList := getCmdList(cmd)
	out, err := exec.Command(execCmd, cmdList...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %s: %v", cmd, err)
	}
	return net.ParseIP(strings.TrimSpace(string(out))), nil
}

func via(dest net.IP) (net.IP, error) {
	prefix := "ip"
	if dest.To4() == nil {
		prefix = "ip -6"
	}
	cmd := fmt.Sprintf(`%s route get %s | grep via | cut -d" " -f3`, prefix, dest.String())
	cmdList := getCmdList(cmd)
	out, err := exec.Command(execCmd, cmdList...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %s: %v", cmd, err)
	}
	if string(out) == "" {
		// assume the provided dest is the next hop
		return dest, nil
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
	return nil, fmt.Errorf("unable to find local address")
}

func addLoopback(name string, addr *net.IPNet) error {
	deleteLoopback(addr)
	prefixLen, _ := addr.Mask.Size()
	label := fmt.Sprintf("lo:%s", name)
	// linux kernel limits labels to 15 chars
	if len(label) > 15 {
		label = label[:15]
	}
	prefix := "ip"
	if addr.IP.To4() == nil {
		prefix = "ip -6"
	}
	cmd := fmt.Sprintf("%s address add %s/%d dev lo label %s", prefix, addr.IP.String(), prefixLen, label)
	cmdList := getCmdList(cmd)
	_, err := exec.Command(execCmd, cmdList...).Output()
	if err != nil {
		return fmt.Errorf("failed to Add loopback command: %s: %v", cmd, err)
	}
	return nil
}

func deleteLoopback(addr *net.IPNet) error {
	prefix := "ip"
	if addr.IP.To4() == nil {
		prefix = "ip -6"
	}
	prefixLen, _ := addr.Mask.Size()
	cmd := fmt.Sprintf("%s address delete %s/%d dev lo", prefix, addr.IP.String(), prefixLen)
	cmdList := getCmdList(cmd)
	_, err := exec.Command(execCmd, cmdList...).Output()
	if err != nil {
		return fmt.Errorf("failed to delete loopback command: %s: %v", cmd, err)
	}
	return nil
}

func natRule(op string, vip, localAddr net.IP, protocol, lport, dport string) error {
	prefix := "iptables"
	if vip.To4() == nil {
		prefix = "ip6tables"
	}
	cmd := fmt.Sprintf(
		"%s -t nat -%s PREROUTING -p %s -d %s --dport %s -j DNAT --to-destination %s:%s",
		prefix, op, protocol, vip.String(), lport, localAddr.String(), dport,
	)
	cmdList := getCmdList(cmd)
	_, err := exec.Command(execCmd, cmdList...).Output()
	if err != nil {
		return fmt.Errorf("Failed to %s nat rule: %s: %v", op, cmd, err)
	}
	return nil
}
