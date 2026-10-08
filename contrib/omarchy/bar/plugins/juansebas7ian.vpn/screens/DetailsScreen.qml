import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"
import "../parts"

// Everything about the active tunnel, and what the internet sees.
Column {
  id: det
  property var vpn: null
  spacing: Style.space(6)

  readonly property bool on: !!vpn && vpn.connected
  readonly property var c: on ? vpn.current : null
  readonly property var loc: on ? vpn.currentLoc : null
  readonly property var ip4: vpn ? vpn.ip4 : null
  readonly property var ip6: vpn ? vpn.ip6 : null

  // ---- header card
  RowLayout {
    width: parent.width
    spacing: Style.space(12)
    Flag { visible: det.on; cc: det.loc ? det.loc.cc : ""; size: 34 }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text {
        text: det.on ? det.vpn.locLabel(det.loc) : "Not connected"
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.body
        font.bold: true
      }
      Text {
        text: det.on ? det.vpn.providerLabel(det.loc.provider) + (det.c.since ? " · connected " + det.vpn.duration(det.c.since) : "")
          : "Your traffic goes out through your provider"
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
  }

  Section { visible: det.on; title: "TUNNEL" }
  Pair { visible: det.on; label: "Protocol"; value: det.c ? (det.c.type === "wireguard" ? "WireGuard" : det.c.type) : "" }
  Pair { visible: det.on && !!det.loc && det.loc.provider === "surfshark"; label: "Server"; value: det.loc ? det.loc.id || "" : "" }
  Pair { visible: det.on && !!det.c.endpoint; label: "Endpoint"; value: det.c && det.c.endpoint ? det.c.endpoint : "" }
  Pair { visible: det.on; label: "Interface"; value: det.c ? det.c.device : "" }
  Pair { visible: det.on && !!det.c.ipv4_address; label: "Tunnel IP"; value: det.c && det.c.ipv4_address ? det.c.ipv4_address.join(", ") : "" }
  Pair { visible: det.on && !!det.c.ipv4_dns; label: "DNS"; value: det.c && det.c.ipv4_dns ? det.c.ipv4_dns.join(", ") : "" }
  Pair { visible: det.on; label: "DNS leaks"; value: "blocked (tunnel DNS only)" }
  Pair { visible: det.on && det.c.rx !== null && det.c.rx !== undefined; label: "Downloaded"; value: det.c ? Theme.bytes(det.c.rx) : "" }
  Pair { visible: det.on && det.c.tx !== null && det.c.tx !== undefined; label: "Uploaded"; value: det.c ? Theme.bytes(det.c.tx) : "" }

  Section { visible: det.on && !!det.loc.region; title: "SERVER" }
  Pair { visible: det.on && !!det.loc.region; label: "Region"; value: det.loc ? det.loc.region : "" }
  Pair { visible: det.on && det.loc.load !== null && det.loc.load !== undefined; label: "Load"; value: det.loc && det.loc.load !== null ? det.loc.load + "%" : "" }

  Section { title: "WHAT WEBSITES SEE" }
  Pair { label: "IPv4"; value: det.ip4 ? det.ip4.ip : (det.vpn && det.vpn.st && det.vpn.st.ip.ipv4Error ? "offline" : "…") }
  Pair { visible: !!det.ip4; label: "Location"; value: det.ip4 ? (det.ip4.city ? det.ip4.city + ", " : "") + det.ip4.country : "" }
  Pair { visible: !!det.ip4; label: "Network"; value: det.ip4 ? det.ip4.organization || "" : "" }
  Pair {
    label: "IPv6"
    hot: !!det.vpn && det.vpn.leak
    value: det.ip6 ? det.ip6.ip : (det.on ? "blocked (no leak)" : "not available")
  }
  Notice {
    visible: !!det.vpn && det.vpn.leak
    level: "crit"
    text: "IPv6 goes around the tunnel (" + (det.ip6 ? det.ip6.organization : "") + "). Reconnect from this panel: its profiles route IPv6 into the tunnel. Profiles made by the official apps do not."
  }
  Caption {
    text: det.vpn && det.vpn.st ? "Checked " + Theme.ago(det.vpn.st.ip.ts) + " via am.i.mullvad.net · r to check again" : ""
  }

  Section { title: "ACTIONS" }
  ActionRow {
    icon: "󰑐"
    label: "Check the public address again"
    hint: "Asks am.i.mullvad.net for IPv4 and IPv6"
    onActivated: det.vpn.refresh(true)
  }
  ActionRow {
    visible: det.on
    icon: "󰅖"
    label: "Disconnect"
    hint: "Back to the normal connection"
    onActivated: det.vpn.disconnect()
  }
  ActionRow {
    visible: det.on
    icon: "󰍎"
    label: "Change location"
    hint: "Picking another one switches without dropping to the open internet for long"
    onActivated: det.vpn.go("locations")
  }
}
