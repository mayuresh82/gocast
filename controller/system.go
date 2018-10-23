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
	mask := fmt.Sprintf("%d.%d.%d.%d", addr.Mask[0], addr.Mask[1], addr.Mask[2], addr.Mask[3])
	cmd := fmt.Sprintf("ifconfig lo:%s %s netmask %s up", name, addr.IP.String(), mask)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to Add loopback command: %v", err)
	}
	return nil
}

func deleteLoopback(name string) error {
	cmd := fmt.Sprintf("ifconfig lo:%s down", name)
	_, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("Failed to delete loopback command: %v", err)
	}
	return nil
}
