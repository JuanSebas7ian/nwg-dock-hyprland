import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"
import "../parts"

// Home: where the traffic comes out now, one big connect button, the
// location picker and the way to the other screens.
Column {
  id: home
  property var vpn: null
  spacing: Style.space(10)

  readonly property bool on: !!vpn && vpn.connected
  readonly property var loc: on ? vpn.currentLoc : (vpn ? vpn.target : null)
  readonly property bool working: !!vpn && ["", "import", "delete", "forget"].indexOf(vpn.busy) < 0

  // ---- status card
  Rectangle {
    width: parent.width
    implicitHeight: card.implicitHeight + Style.space(28)
    radius: Style.cornerRadius
    color: Qt.alpha(home.vpn && home.vpn.leak ? Theme.urgent : Theme.foreground, home.on ? 0.10 : 0.04)
    border.width: 1
    border.color: Qt.alpha(home.vpn && home.vpn.leak ? Theme.urgent : Theme.foreground, home.on ? 0.35 : 0.12)

    ColumnLayout {
      id: card
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.margins: Style.space(14)
      spacing: Style.space(10)

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(14)
        Flag {
          visible: home.on
          cc: home.loc ? home.loc.cc : ""
          size: 44
        }
        Text {
          visible: !home.on
          text: home.vpn ? home.vpn.glyphOff : ""
          color: Theme.dim
          font.family: Theme.fontFamily
          font.pixelSize: 40
        }
        ColumnLayout {
          Layout.fillWidth: true
          spacing: Style.space(2)
          Text {
            Layout.fillWidth: true
            text: !home.vpn || !home.vpn.st ? "Checking…"
              : home.working ? (home.vpn.busy === "down" ? "Disconnecting…" : "Connecting…")
              : home.on ? (home.loc.city || home.loc.country) : "Not protected"
            color: Theme.foreground
            font.family: Theme.fontFamily
            font.pixelSize: Style.font.display * 0.8
            font.bold: true
            elide: Text.ElideRight
          }
          Text {
            Layout.fillWidth: true
            text: home.on ? (home.loc.city && home.loc.city !== home.loc.country ? home.loc.country + " · " : "") + home.vpn.providerLabel(home.loc.provider)
              : home.vpn && home.vpn.ip4 ? "Your real address is visible: " + home.vpn.ip4.city + ", " + home.vpn.ip4.country
              : ""
            color: Theme.dim
            font.family: Theme.fontFamily
            font.pixelSize: Style.font.caption
            elide: Text.ElideRight
          }
        }
      }

      // Status pill
      Rectangle {
        implicitWidth: pill.implicitWidth + Style.space(16)
        implicitHeight: pill.implicitHeight + Style.space(6)
        radius: height / 2
        color: Qt.alpha(home.vpn && home.vpn.leak ? Theme.urgent : Theme.foreground, home.on ? 0.18 : 0.08)
        Text {
          id: pill
          anchors.centerIn: parent
          text: home.vpn && home.vpn.leak ? "● IPv6 leaking — see details"
            : home.on ? "● Protected" + (home.vpn.ip4 ? " · " + home.vpn.ip4.ip : "")
            : "○ Off"
          color: home.vpn && home.vpn.leak ? Theme.urgent : Theme.foreground
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.caption
          font.bold: true
        }
      }
    }
  }

  // ---- big button
  Button {
    width: parent.width
    text: home.working ? "Working…"
      : home.on ? "Disconnect"
      : home.loc ? "Connect to " + home.vpn.locLabel(home.loc)
      : "Choose a location"
    iconText: home.on ? "󰅖" : home.vpn ? home.vpn.glyphOn : ""
    bordered: true
    selected: !home.on
    foreground: Theme.foreground
    fontFamily: Theme.fontFamily
    fontSize: Style.font.body
    verticalPadding: Style.space(10)
    onClicked: home.vpn.primary()
  }

  // ---- location picker
  Section { title: "LOCATION" }
  NavRow {
    emphasis: true
    showFlag: !!home.loc
    cc: home.loc ? home.loc.cc : ""
    icon: "󰍎"
    label: home.loc ? home.vpn.locLabel(home.loc) : "Choose a location"
    hint: home.loc ? [home.loc.region, home.loc.load !== null && home.loc.load !== undefined ? home.loc.load + "% load" : "", home.vpn.providerLabel(home.loc.provider)].filter(function(s) { return s }).join(" · ")
      : "Surfshark · Proton Free"
    trailing: "Change"
    onActivated: home.vpn.go("locations")
  }

  // ---- what the internet sees
  Section { title: "WHAT WEBSITES SEE" }
  Pair { label: "Public IP"; value: home.vpn && home.vpn.ip4 ? home.vpn.ip4.ip : "…" }
  Pair { label: "Location"; value: home.vpn && home.vpn.ip4 ? (home.vpn.ip4.city ? home.vpn.ip4.city + ", " : "") + home.vpn.ip4.country : "…" }
  Pair { label: "Network"; value: home.vpn && home.vpn.ip4 ? home.vpn.ip4.organization || "" : "" }
  Pair {
    label: "IPv6"
    hot: !!home.vpn && home.vpn.leak
    value: !home.vpn ? "" : home.vpn.leak ? "leaking" : home.vpn.ip6 ? (home.on ? "through the tunnel" : "visible") : (home.on ? "blocked (no leak)" : "not available")
  }

  // ---- more
  Section { title: "MORE" }
  Notice {
    visible: !!home.vpn && home.vpn.inbox.length > 0
    level: "info"
    text: home.vpn ? home.vpn.inbox.length + " WireGuard config(s) in ~/Downloads ready to import — open Accounts & servers." : ""
  }
  NavRow {
    icon: "󰋼"
    label: "Connection details"
    hint: home.on ? "Server, tunnel, DNS, traffic, time connected" : "Your current address and network"
    onActivated: home.vpn.go("details")
  }
  NavRow {
    icon: "󰀉"
    label: "Accounts & servers"
    hint: !home.vpn || !home.vpn.st ? ""
      : (home.vpn.surfsharkReady ? "Surfshark ready" : "Surfshark not set up") + " · "
        + home.vpn.protonServers.length + " Proton server(s)"
    trailing: home.vpn && home.vpn.inbox.length > 0 ? home.vpn.inbox.length + " new" : ""
    onActivated: home.vpn.go("accounts")
  }
  Line { small: true; tone: "dim"; text: "Keys: c connect/disconnect · l locations · i details · a accounts · Esc back" }
}
