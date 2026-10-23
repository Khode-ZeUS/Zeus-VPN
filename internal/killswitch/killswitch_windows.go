// Package killswitch blocks all network traffic except Zeus VPN's own (and,
// in Full VPN mode, traffic through its adapter) using the Windows
// Filtering Platform. The filters live in a dynamic WFP session, so Windows
// removes them automatically if the app exits or crashes.
//
// The WFP plumbing (types, syscalls and most rules) is adapted from
// WireGuard for Windows (MIT, Copyright (C) 2019-2026 WireGuard LLC).
package killswitch

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether this platform has a kill switch.
const Supported = true

type wfpObjectInstaller func(uintptr) error

type baseObjects struct {
	provider windows.GUID
	filters  windows.GUID
}

var (
	mu      sync.Mutex
	session uintptr
)

// Active reports whether the kill switch is currently blocking traffic.
func Active() bool {
	mu.Lock()
	defer mu.Unlock()
	return session != 0
}

// Enable blocks everything except this process, loopback, DHCP/NDP, the
// VPN adapter (tunLUID, 0 if none) and, if allowLAN, private networks.
func Enable(tunLUID uint64, allowLAN bool) error {
	mu.Lock()
	defer mu.Unlock()
	if session != 0 {
		return nil
	}
	s, err := openSession()
	if err != nil {
		return err
	}
	install := func(s uintptr) error {
		bo, err := registerBaseObjects(s)
		if err != nil {
			return err
		}
		if err := permitProcess(s, bo, 15); err != nil {
			return err
		}
		if err := permitLoopback(s, bo, 13); err != nil {
			return err
		}
		if tunLUID != 0 {
			if err := permitTunInterface(s, bo, 12, tunLUID); err != nil {
				return err
			}
		}
		if allowLAN {
			if err := permitLAN(s, bo, 12); err != nil {
				return err
			}
		}
		for _, rule := range []func(uintptr, *baseObjects, uint8) error{permitDHCPIPv4, permitDHCPIPv6, permitNdp} {
			if err := rule(s, bo, 12); err != nil {
				return err
			}
		}
		return blockAll(s, bo, 0)
	}
	if err := runTransaction(s, install); err != nil {
		fwpmEngineClose0(s)
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return errors.New("the kill switch needs administrator rights")
		}
		return err
	}
	session = s
	return nil
}

// Disable removes every filter added by Enable.
func Disable() {
	mu.Lock()
	defer mu.Unlock()
	if session != 0 {
		fwpmEngineClose0(session)
		session = 0
	}
}

func longPathName(p string) (string, error) {
	short, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(short, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

func openSession() (uintptr, error) {
	display, err := createWtFwpmDisplayData0("Zeus VPN", "Zeus VPN kill switch")
	if err != nil {
		return 0, wrapErr(err)
	}
	s := wtFwpmSession0{
		displayData:          *display,
		flags:                cFWPM_SESSION_FLAG_DYNAMIC,
		txnWaitTimeoutInMSec: windows.INFINITE,
	}
	handle := uintptr(0)
	if err := fwpmEngineOpen0(nil, cRPC_C_AUTHN_WINNT, nil, &s, unsafe.Pointer(&handle)); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return 0, errors.New("the kill switch needs administrator rights")
		}
		return 0, wrapErr(err)
	}
	return handle, nil
}

func registerBaseObjects(s uintptr) (*baseObjects, error) {
	bo := &baseObjects{}
	var err error
	if bo.provider, err = windows.GenerateGUID(); err != nil {
		return nil, wrapErr(err)
	}
	if bo.filters, err = windows.GenerateGUID(); err != nil {
		return nil, wrapErr(err)
	}
	display, err := createWtFwpmDisplayData0("Zeus VPN", "Zeus VPN provider")
	if err != nil {
		return nil, wrapErr(err)
	}
	provider := wtFwpmProvider0{providerKey: bo.provider, displayData: *display}
	if err := fwpmProviderAdd0(s, &provider, 0); err != nil {
		return nil, wrapErr(err)
	}
	display, err = createWtFwpmDisplayData0("Zeus VPN filters", "Kill switch permit and block filters")
	if err != nil {
		return nil, wrapErr(err)
	}
	sublayer := wtFwpmSublayer0{subLayerKey: bo.filters, displayData: *display, providerKey: &bo.provider, weight: ^uint16(0)}
	if err := fwpmSubLayerAdd0(s, &sublayer, 0); err != nil {
		return nil, wrapErr(err)
	}
	return bo, nil
}

// addFilter installs filter on each layer.
func addFilter(s uintptr, filter wtFwpmFilter0, name string, layers ...windows.GUID) error {
	display, err := createWtFwpmDisplayData0(name, "")
	if err != nil {
		return wrapErr(err)
	}
	filter.displayData = *display
	id := uint64(0)
	for _, layer := range layers {
		filter.layerKey = layer
		if err := fwpmFilterAdd0(s, &filter, 0, &id); err != nil {
			return wrapErr(err)
		}
	}
	return nil
}

// permitProcess lets this executable reach the network, so it can keep
// reconnecting while everything else is blocked.
func permitProcess(s uintptr, bo *baseObjects, weight uint8) error {
	appID, err := getCurrentProcessAppID()
	if err != nil {
		return err
	}
	defer fwpmFreeMemory0(unsafe.Pointer(&appID))
	condition := wtFwpmFilterCondition0{
		fieldKey:  cFWPM_CONDITION_ALE_APP_ID,
		matchType: cFWP_MATCH_EQUAL,
		conditionValue: wtFwpConditionValue0{
			_type: cFWP_BYTE_BLOB_TYPE,
			value: uintptr(unsafe.Pointer(appID)),
		},
	}
	filter := wtFwpmFilter0{
		providerKey:         &bo.provider,
		subLayerKey:         bo.filters,
		weight:              filterWeight(weight),
		flags:               cFWPM_FILTER_FLAG_CLEAR_ACTION_RIGHT,
		numFilterConditions: 1,
		filterCondition:     &condition,
		action:              wtFwpmAction0{_type: cFWP_ACTION_PERMIT},
	}
	return addFilter(s, filter, "Permit Zeus VPN",
		cFWPM_LAYER_ALE_AUTH_CONNECT_V4, cFWPM_LAYER_ALE_AUTH_RECV_ACCEPT_V4,
		cFWPM_LAYER_ALE_AUTH_CONNECT_V6, cFWPM_LAYER_ALE_AUTH_RECV_ACCEPT_V6)
}

// permitLAN keeps printers, routers and other local devices reachable.
func permitLAN(s uintptr, bo *baseObjects, weight uint8) error {
	ranges := []wtFwpV4AddrAndMask{
		{0x0A000000, 0xFF000000}, // 10.0.0.0/8
		{0xAC100000, 0xFFF00000}, // 172.16.0.0/12
		{0xC0A80000, 0xFFFF0000}, // 192.168.0.0/16
		{0xA9FE0000, 0xFFFF0000}, // 169.254.0.0/16
		{0xE0000000, 0xF0000000}, // 224.0.0.0/4 (local discovery)
	}
	conditions := make([]wtFwpmFilterCondition0, len(ranges))
	for i := range ranges {
		conditions[i] = wtFwpmFilterCondition0{
			fieldKey:  cFWPM_CONDITION_IP_REMOTE_ADDRESS,
			matchType: cFWP_MATCH_EQUAL,
			conditionValue: wtFwpConditionValue0{
				_type: cFWP_V4_ADDR_MASK,
				value: uintptr(unsafe.Pointer(&ranges[i])),
			},
		}
	}
	filter := wtFwpmFilter0{
		providerKey:         &bo.provider,
		subLayerKey:         bo.filters,
		weight:              filterWeight(weight),
		numFilterConditions: uint32(len(conditions)),
		filterCondition:     &conditions[0],
		action:              wtFwpmAction0{_type: cFWP_ACTION_PERMIT},
	}
	return addFilter(s, filter, "Permit local network", cFWPM_LAYER_ALE_AUTH_CONNECT_V4, cFWPM_LAYER_ALE_AUTH_RECV_ACCEPT_V4)
}
