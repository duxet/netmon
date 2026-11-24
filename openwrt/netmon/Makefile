#
# OpenWrt package for netmon.
#
# netmon links DuckDB, whose prebuilt libraries are built for glibc, so the
# SDK (musl) can't compile it. This package therefore installs the statically
# linked Linux binary built by .github/workflows/build.yml instead of building
# anything. Put that binary at src/netmon (next to this Makefile) before
# building; the workflow does so for every architecture.
#
# PKG_VERSION is a placeholder: the workflow sets it from the release tag.
#

include $(TOPDIR)/rules.mk

PKG_NAME:=netmon
PKG_VERSION:=0.0.0
PKG_RELEASE:=1

include $(INCLUDE_DIR)/package.mk

define Package/netmon
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Home network traffic monitor
  URL:=https://github.com/duxet/netmon
  # DuckDB has no 32-bit builds, so only 64-bit targets can run netmon.
  # Traffic is read from conntrack over netlink.
  DEPENDS:=@(aarch64||x86_64) +kmod-nf-conntrack-netlink
endef

define Package/netmon/description
  Monitors the traffic of the devices on your network by reading the
  connection tracking counters of the router, and serves a web UI and JSON
  API on port 2137.

  The binary is large (about 90 MB installed), because it embeds the DuckDB
  database. Make sure there is room for it, and keep the traffic database on
  external storage (see the data_dir option in /etc/config/netmon).
endef

define Package/netmon/conffiles
/etc/config/netmon
endef

define Build/Prepare
	test -f ./src/netmon || { \
		echo "netmon: put the static netmon binary for $(ARCH) at src/netmon" >&2; \
		exit 1; \
	}
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) ./src/netmon $(PKG_BUILD_DIR)/
endef

define Build/Configure
endef

define Build/Compile
endef

define Package/netmon/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/netmon $(1)/usr/bin/netmon
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/netmon.init $(1)/etc/init.d/netmon
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/netmon.config $(1)/etc/config/netmon
endef

$(eval $(call BuildPackage,netmon))
