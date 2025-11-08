package src

const (
	RESET  = "\033[0m"
	BLUE   = "\033[1;34m"
	GREEN  = "\033[1;32m"
	RED    = "\033[1;31m"
	YELLOW = "\033[1;33m"
	CYAN   = "\033[1;36m"
)

const (
    SCRIPT_VERSION                    = "GO_V1.0.0"
    REMOTE_SCRIPT_URL                 = "https://raw.githubusercontent.com/kilvil/oneclick_smartdns/main/smartdns_install.sh"
    REMOTE_STREAM_CONFIG_FILE_URL     = "https://raw.githubusercontent.com/kilvil/oneclick_smartdns/main/StreamConfig.yaml"
    REMOTE_SMARTDNS_URL               = "https://github.com/pymumu/smartdns/releases/download/Release46/smartdns.1.2024.06.12-2222.x86-linux-all.tar.gz"
    REMOTE_RegionRestrictionCheck_URL = "https://raw.githubusercontent.com/1-stream/RegionRestrictionCheck/main/check.sh"
    REMOTE_SNIPROXY_INSTALLER_URL     = "https://raw.githubusercontent.com/lthero-big/Smartdns_sniproxy_installer/refs/heads/main/smtdns_install.sh"

	SMART_CONFIG_FILE = "/etc/smartdns/smartdns.conf"
	SNIPROXY_CONFIG   = "/etc/sniproxy.conf"
)

const defaultSmartDNSConfig = `bind [::]:53

dualstack-ip-selection no
speed-check-mode none
serve-expired-prefetch-time 21600
prefetch-domain yes
cache-size 32768
cache-persist yes
cache-file /etc/smartdns/cache
prefetch-domain yes
serve-expired yes
serve-expired-ttl 259200
serve-expired-reply-ttl 3
prefetch-domain yes
serve-expired-prefetch-time 21600
cache-checkpoint-time 86400

# 默认上游 DNS
server 8.8.8.8
server 8.8.4.4
`
