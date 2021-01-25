package dcnet

import (
	"fmt"

	"github.com/coreos/go-iptables/iptables"
	"github.com/cybozu-go/log"
)

// CreateNatRules creates nat rules with iptables
func CreateNatRules() error {
	ipt4, ipt6, err := NewIptables()
	if err != nil {
		return err
	}

	for _, ipt := range []*iptables.IPTables{ipt4, ipt6} {
		err = ipt.NewChain("filter", "PLACEMAT")
		if err != nil {
			return fmt.Errorf("failed to create the new chain in filter table: %w", err)
		}
		err = ipt.NewChain("nat", "PLACEMAT")
		if err != nil {
			return fmt.Errorf("failed to create the new chain in nat table: %w", err)
		}

		err = ipt.Append("nat", "POSTROUTING", "-j", "PLACEMAT")
		if err != nil {
			return fmt.Errorf("failed to append the PLACEMAT rule in nat table: %w", err)
		}
		err = ipt.Append("filter", "FORWARD", "-j", "PLACEMAT")
		if err != nil {
			return fmt.Errorf("failed to append the PLACEMAT rule in filter table: %w", err)
		}
	}

	return nil
}

// CleanupNatRules destroys nat rules
func CleanupNatRules() {
	ipt4, ipt6, err := NewIptables()
	if err != nil {
		log.Warn("failed to new IpTables", map[string]interface{}{
			log.FnError: err,
		})
	}

	for _, ipt := range []*iptables.IPTables{ipt4, ipt6} {
		err := ipt.Delete("filter", "FORWARD", "-j", "PLACEMAT")
		if err != nil {
			log.Warn("failed to delete the PLACEMAT rule in filter table", map[string]interface{}{
				log.FnError: err,
			})
		}
		err = ipt.Delete("nat", "POSTROUTING", "-j", "PLACEMAT")
		if err != nil {
			log.Warn("failed to delete the PLACEMAT rule in nat table", map[string]interface{}{
				log.FnError: err,
			})
		}

		err = ipt.ClearChain("filter", "PLACEMAT")
		if err != nil {
			log.Warn("failed to clear the PLACEMAT chain in filter table", map[string]interface{}{
				log.FnError: err,
			})
		}
		err = ipt.DeleteChain("filter", "PLACEMAT")
		if err != nil {
			log.Warn("failed to delete the PLACEMAT chain in filter table", map[string]interface{}{
				log.FnError: err,
			})
		}

		err = ipt.ClearChain("nat", "PLACEMAT")
		if err != nil {
			log.Warn("failed to clear the PLACEMAT chain in nat table", map[string]interface{}{
				log.FnError: err,
			})
		}
		err = ipt.DeleteChain("nat", "PLACEMAT")
		if err != nil {
			log.Warn("failed to delete the PLACEMAT chain in nat table", map[string]interface{}{
				log.FnError: err,
			})
		}
	}
}

// NewIptables creates IPTables
func NewIptables() (*iptables.IPTables, *iptables.IPTables, error) {
	ipt4, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create iptables for IPv4: %w", err)
	}
	ipt6, err := iptables.NewWithProtocol(iptables.ProtocolIPv6)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create iptables for IPv6: %w", err)
	}
	return ipt4, ipt6, err
}
