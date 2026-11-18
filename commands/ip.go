package commands

import (
	"errors"
	"net"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("ip", runIP) }

func runIP(c *core.Context) error {
	fs := c.Flags()
	first := fs.Bool("1", false, "print only the first IPv4 address")
	v6 := fs.Bool("6", false, "include IPv6 addresses")
	if _, err := c.Parse(fs); err != nil {
		return err
	}
	addrs, err := localAddrs(*v6)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return errors.New("no network address found: are you offline?")
	}
	if *first {
		c.Println(addrs[0][1])
		return nil
	}
	for _, a := range addrs {
		c.Printf("%-10s %s\n", a[0], a[1])
	}
	return nil
}

// localAddrs returns [interface, address] pairs for every interface that is
// up and not loopback. IPv4 addresses come first.
func localAddrs(includeV6 bool) ([][2]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var v4, v6 [][2]string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				v4 = append(v4, [2]string{ifc.Name, ip4.String()})
			} else if includeV6 {
				v6 = append(v6, [2]string{ifc.Name, ipnet.IP.String()})
			}
		}
	}
	return append(v4, v6...), nil
}
