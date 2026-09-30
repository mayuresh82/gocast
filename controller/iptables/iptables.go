package iptables

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/golang/glog"
)

// Action signifies the iptable action.
type Action string

// Table refers to Nat, Filter or Mangle.
type Table string

// Chain refers to the various chains in iptable
type Chain string

const (
	// Append appends the rule at the end of the chain.
	Append Action = "-A"
	// Delete deletes the rule from the chain.
	Delete Action = "-D"
	// Insert inserts the rule at the top of the chain.
	Insert Action = "-I"
	// Nat table is used for nat translation rules.
	Nat Table = "nat"
	// PREROUTING is the built-in PREROUTING chain
	PREROUTING Chain = "PREROUTING"
	// OUTPUT is the built-in OUTPUT chain
	OUTPUT Chain = "OUTPUT"
)

var iptablesPath string

func init() {
	path, err := exec.LookPath("iptables")
	if err != nil {
		glog.Exitf("unable to find path for iptables: %v\n", err)
		return
	}
	iptablesPath = path
}

func Raw(args ...string) (string, error) {
	if l := glog.V(4); l {
		l.Infof("iptables executing %s", strings.Join(args, " "))
	}
	output, err := exec.Command(iptablesPath, args...).CombinedOutput()
	if err != nil {
		return "", err
	}

	return string(output), err
}

func AddChain(table Table, name Chain) error {
	if table == "" {
		table = Nat
	}

	if _, err := Raw("-t", string(table), "-n", "-L", string(name)); err != nil {
		if output, err := Raw("-t", string(table), "-N", string(name)); err != nil {
			return err
		} else if len(output) != 0 {
			return fmt.Errorf("could not create %s/%s chain: %s", table, name, output)
		}
	}
	return nil
}

func RemoveChain(table Table, name Chain) error {
	if table == "" {
		table = Nat
	}

	_, _ = Raw("-t", string(table), "-F", string(name))
	if output, err := Raw("-t", string(table), "-X", string(name)); err != nil {
		return err
	} else if len(output) != 0 {
		return fmt.Errorf("could not remove %s/%s chain: %s", table, name, output)
	}
	return nil

}

func (c Chain) Add(table Table, args ...string) error {
	if table == "" {
		table = Nat
	}

	if output, err := Raw(append([]string{"-t", string(table), string(Append), string(c)}, args...)...); err != nil {
		return err
	} else if len(output) != 0 {
		return fmt.Errorf("could not add rule %q on %s/%s: %s", strings.Join(args, " "), table, c, output)
	}
	return nil
}

func (c Chain) Remove(table Table, args ...string) error {
	if table == "" {
		table = Nat
	}

	if output, err := Raw(append([]string{"-t", string(table), string(Delete), string(c)}, args...)...); err != nil {
		return err
	} else if len(output) != 0 {
		return fmt.Errorf("could not delete rule %q on %s/%s: %s", strings.Join(args, " "), table, c, output)
	}
	return nil
}
