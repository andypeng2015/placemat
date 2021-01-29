package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cybozu-go/placemat/v2/pkg/dcnet"
	"github.com/cybozu-go/placemat/v2/pkg/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("QEMU command builder", func() {
	BeforeEach(func() {
		Expect(dcnet.CreateNatRules()).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		dcnet.CleanupNatRules()
	})

	It("should build a QEMU command which runs a virtual machine as specified", func() {
		// Set up runtime
		LoadModules()
		cur, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		temp := filepath.Join(cur, "temp")
		Expect(os.Mkdir(temp, 0755)).NotTo(HaveOccurred())
		r, err := NewRuntime(false, false, filepath.Join(temp, "run"), filepath.Join(temp, "data"),
			filepath.Join(temp, "cache"), "127.0.0.1:10808")
		Expect(err).NotTo(HaveOccurred())

		// Create dummy files and directories
		_, err = os.Create("temp/cybozu-ubuntu-18.04-server-cloudimg-amd64.img")
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Create("temp/seed_boot-0.yml")
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Create("temp/network.yml")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Mkdir("temp/shared-dir", 0755)).NotTo(HaveOccurred())
		sharedDir, err := filepath.Abs("temp/shared-dir")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(temp)

		clusterYaml := fmt.Sprintf(`
kind: Node
name: boot-0
cpu: 8
memory: 2G
interfaces:
- r0-node1
- r0-node2
volumes:
- cache: writeback
  copy-on-write: true
  image: custom-ubuntu-image
  kind: image
  name: root
- kind: localds
  name: seed
  network-config: temp/network.yml
  user-data: temp/seed_boot-0.yml
- kind: hostPath
  name: sabakan
  path: %s
smbios:
  serial: fb8f2417d0b4db30050719c31ce02a2e8141bbd8
---
kind: Image
name: custom-ubuntu-image
file: temp/cybozu-ubuntu-18.04-server-cloudimg-amd64.img
---
kind: Network
name: r0-node1
type: internal
use-nat: false
---
kind: Network
name: r0-node2
type: internal
use-nat: false
`, sharedDir)
		cluster, err := types.Parse(strings.NewReader(clusterYaml))
		Expect(err).NotTo(HaveOccurred())

		// Create bridges
		var networks []dcnet.Network
		for _, n := range cluster.Networks {
			network, err := dcnet.NewNetwork(n)
			Expect(err).NotTo(HaveOccurred())
			networks = append(networks, network)
			Expect(network.Setup(1460, false)).NotTo(HaveOccurred())
		}
		defer func() {
			for _, n := range networks {
				n.Cleanup()
			}
		}()

		nodeSpec := cluster.Nodes[0]

		// Create volumes
		var volumeArgs []volumeArgs
		for _, volumeSpec := range nodeSpec.Volumes {
			volume, err := newNodeVolume(volumeSpec, cluster.Images)
			Expect(err).NotTo(HaveOccurred())

			args, err := volume.create(context.Background(), r.DataDir)
			Expect(err).NotTo(HaveOccurred())
			volumeArgs = append(volumeArgs, args)
		}

		// Create taps
		var taps []*tap
		var tapInfos []*tapInfo
		for _, i := range nodeSpec.Interfaces {
			tap, err := newTap(i)
			Expect(err).NotTo(HaveOccurred())
			taps = append(taps, tap)

			tapInfo, err := tap.create(1460)
			Expect(err).NotTo(HaveOccurred())
			tapInfos = append(tapInfos, tapInfo)
		}
		defer func() {
			for _, tap := range taps {
				tap.Cleanup()
			}
		}()

		qemu := newQemu(nodeSpec.Name, tapInfos, volumeArgs, nodeSpec.IgnitionFile, nodeSpec.CPU, nodeSpec.Memory,
			nodeSpec.UEFI, nodeSpec.TPM, smBIOSConfig{
				manufacturer: nodeSpec.SMBIOS.Manufacturer,
				product:      nodeSpec.SMBIOS.Product,
				serial:       nodeSpec.SMBIOS.Serial,
			})
		qemu.macGenerator = &macGeneratorMock{}
		command := qemu.command(r)

		expected := strings.ReplaceAll(fmt.Sprintf(`
qemu-system-x86_64
 -enable-kvm
 -smp 8
 -m 2G
 -nographic
 -serial unix:%s/boot-0.socket,server,nowait
 -smbios type=1,serial=fb8f2417d0b4db30050719c31ce02a2e8141bbd8
 -netdev tap,id=r0-node1,ifname=%s,script=no,downscript=no,vhost=on
 -device virtio-net-pci,host_mtu=1460,netdev=r0-node1,mac=placemat
 -netdev tap,id=r0-node2,ifname=%s,script=no,downscript=no,vhost=on
 -device virtio-net-pci,host_mtu=1460,netdev=r0-node2,mac=placemat
 -drive if=virtio,cache=writeback,aio=threads,file=%s/root.img
 -drive if=virtio,cache=none,aio=native,format=raw,file=%s/seed.img
 -virtfs local,path=%s,mount_tag=sabakan,security_model=none,readonly
 -boot reboot-timeout=30000
 -chardev socket,id=char0,path=%s/boot-0.guest,server,nowait
 -device virtio-serial
 -device virtserialport,chardev=char0,name=placemat
 -monitor unix:%s/boot-0.monitor,server,nowait
 -object rng-random,id=rng0,filename=/dev/urandom
 -device virtio-rng-pci,rng=rng0
 -cpu host
`, r.RunDir, tapInfos[0].tap, tapInfos[1].tap, r.DataDir, r.DataDir, sharedDir, r.RunDir, r.RunDir), "\n", "")
		actual := strings.Join(command, " ")
		Expect(actual).To(Equal(expected))
	})

	It("should build a QEMU command with uefi and tpm enabled", func() {
		// Set up runtime
		LoadModules()
		cur, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		temp := filepath.Join(cur, "temp")
		Expect(os.Mkdir(temp, 0755)).NotTo(HaveOccurred())
		r, err := NewRuntime(false, false, filepath.Join(temp, "run"), filepath.Join(temp, "data"),
			filepath.Join(temp, "cache"), "127.0.0.1:10808")
		Expect(err).NotTo(HaveOccurred())

		// Create dummy files and directories
		_, err = os.Create("temp/cybozu-ubuntu-18.04-server-cloudimg-amd64.img")
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Create("temp/seed_boot-0.yml")
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Create("temp/network.yml")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Mkdir("temp/shared-dir", 0755)).NotTo(HaveOccurred())
		sharedDir, err := filepath.Abs("temp/shared-dir")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(temp)

		clusterYaml := fmt.Sprintf(`
kind: Node
name: boot-0
cpu: 8
memory: 2G
interfaces:
- r0-node1
- r0-node2
volumes:
- cache: writeback
  copy-on-write: true
  image: custom-ubuntu-image
  kind: image
  name: root
- kind: localds
  name: seed
  network-config: temp/network.yml
  user-data: temp/seed_boot-0.yml
- kind: hostPath
  name: sabakan
  path: %s
smbios:
  serial: fb8f2417d0b4db30050719c31ce02a2e8141bbd8
UEFI: true
TPM: true
---
kind: Image
name: custom-ubuntu-image
file: temp/cybozu-ubuntu-18.04-server-cloudimg-amd64.img
---
kind: Network
name: r0-node1
type: internal
use-nat: false
---
kind: Network
name: r0-node2
type: internal
use-nat: false
`, sharedDir)
		cluster, err := types.Parse(strings.NewReader(clusterYaml))
		Expect(err).NotTo(HaveOccurred())

		// Create bridges
		var networks []dcnet.Network
		for _, n := range cluster.Networks {
			network, err := dcnet.NewNetwork(n)
			Expect(err).NotTo(HaveOccurred())
			networks = append(networks, network)
			Expect(network.Setup(1460, false)).NotTo(HaveOccurred())
		}
		defer func() {
			for _, n := range networks {
				n.Cleanup()
			}
		}()

		nodeSpec := cluster.Nodes[0]

		// Create volumes
		var volumeArgs []volumeArgs
		for _, volumeSpec := range nodeSpec.Volumes {
			volume, err := newNodeVolume(volumeSpec, cluster.Images)
			Expect(err).NotTo(HaveOccurred())

			args, err := volume.create(context.Background(), r.DataDir)
			Expect(err).NotTo(HaveOccurred())
			volumeArgs = append(volumeArgs, args)
		}

		// Create taps
		var taps []*tap
		var tapInfos []*tapInfo
		for _, i := range nodeSpec.Interfaces {
			tap, err := newTap(i)
			Expect(err).NotTo(HaveOccurred())
			taps = append(taps, tap)

			tapInfo, err := tap.create(1460)
			Expect(err).NotTo(HaveOccurred())
			tapInfos = append(tapInfos, tapInfo)
		}
		defer func() {
			for _, tap := range taps {
				tap.Cleanup()
			}
		}()

		qemu := newQemu(nodeSpec.Name, tapInfos, volumeArgs, nodeSpec.IgnitionFile, nodeSpec.CPU, nodeSpec.Memory,
			nodeSpec.UEFI, nodeSpec.TPM, smBIOSConfig{
				manufacturer: nodeSpec.SMBIOS.Manufacturer,
				product:      nodeSpec.SMBIOS.Product,
				serial:       nodeSpec.SMBIOS.Serial,
			})
		qemu.macGenerator = &macGeneratorMock{}
		command := qemu.command(r)

		expected := strings.ReplaceAll(fmt.Sprintf(`
qemu-system-x86_64
 -enable-kvm
 -smp 8
 -m 2G
 -nographic
 -serial unix:%s/boot-0.socket,server,nowait
 -drive if=pflash,file=/usr/share/OVMF/OVMF_CODE.fd,format=raw,readonly
 -drive if=pflash,file=%s/nvram/boot-0.fd,format=raw
 -smbios type=1,serial=fb8f2417d0b4db30050719c31ce02a2e8141bbd8
 -netdev tap,id=r0-node1,ifname=%s,script=no,downscript=no,vhost=on
 -device virtio-net-pci,host_mtu=1460,netdev=r0-node1,mac=placemat,romfile=
 -netdev tap,id=r0-node2,ifname=%s,script=no,downscript=no,vhost=on
 -device virtio-net-pci,host_mtu=1460,netdev=r0-node2,mac=placemat,romfile=
 -drive if=virtio,cache=writeback,aio=threads,file=%s/root.img
 -drive if=virtio,cache=none,aio=native,format=raw,file=%s/seed.img
 -virtfs local,path=%s,mount_tag=sabakan,security_model=none,readonly
 -chardev socket,id=chrtpm,path=%s/boot-0/swtpm.socket
 -tpmdev emulator,id=tpm0,chardev=chrtpm
 -device tpm-tis,tpmdev=tpm0
 -boot reboot-timeout=30000
 -chardev socket,id=char0,path=%s/boot-0.guest,server,nowait
 -device virtio-serial
 -device virtserialport,chardev=char0,name=placemat
 -monitor unix:%s/boot-0.monitor,server,nowait
 -object rng-random,id=rng0,filename=/dev/urandom
 -device virtio-rng-pci,rng=rng0
 -cpu host
`, r.RunDir, r.DataDir, tapInfos[0].tap, tapInfos[1].tap, r.DataDir, r.DataDir, sharedDir, r.RunDir, r.RunDir, r.RunDir), "\n", "")
		actual := strings.Join(command, " ")
		Expect(actual).To(Equal(expected))
	})
})

type macGeneratorMock struct {
}

func (m *macGeneratorMock) generate() string {
	return "placemat"
}
