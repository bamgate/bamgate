module github.com/kuuji/bamgate

go 1.25.5

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/coder/websocket v1.8.14
	github.com/google/nftables v0.3.0
	github.com/pion/transport/v4 v4.0.1
	github.com/pion/webrtc/v4 v4.2.6
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.48.0
	golang.org/x/mobile v0.0.0-20260211191516-dcd2a3258864
	golang.org/x/sys v0.41.0
	golang.zx2c4.com/wireguard v0.0.0-20250521234502-f333402bd9cb
)

require (
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mdlayher/netlink v1.7.3-0.20250113171957-fbb4dce95f42 // indirect
	github.com/mdlayher/socket v0.5.0 // indirect
	github.com/pion/datachannel v1.6.0 // indirect
	github.com/pion/dtls/v3 v3.1.2 // indirect
	github.com/pion/ice/v4 v4.2.0 // indirect
	github.com/pion/interceptor v0.1.44 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.1.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.16 // indirect
	github.com/pion/rtp v1.10.1 // indirect
	github.com/pion/sctp v1.9.2 // indirect
	github.com/pion/sdp/v3 v3.0.17 // indirect
	github.com/pion/srtp/v3 v3.0.10 // indirect
	github.com/pion/stun/v3 v3.1.1 // indirect
	github.com/pion/turn/v4 v4.1.4 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/mod v0.33.0 // indirect
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/time v0.10.0 // indirect
	golang.org/x/tools v0.42.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
)

replace github.com/wlynxg/anet => ./third_party/anet
