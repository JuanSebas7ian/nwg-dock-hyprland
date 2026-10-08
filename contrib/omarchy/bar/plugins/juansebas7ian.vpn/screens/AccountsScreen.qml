import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"
import "../parts"

// Accounts & servers: the Surfshark key, the imported Proton servers, the
// official apps and anything else NetworkManager knows as a VPN.
Column {
  id: acc
  property var vpn: null
  spacing: Style.space(8)

  readonly property var st: vpn ? vpn.st : null
  readonly property int inbox: vpn ? vpn.inbox.length : 0

  // ---- import
  Notice {
    visible: acc.inbox > 0
    level: "info"
    text: acc.inbox + " WireGuard config(s) found in ~/Downloads."
  }
  Button {
    visible: acc.inbox > 0
    width: parent.width
    text: acc.vpn && acc.vpn.busy === "import" ? "Importing…" : "Import " + acc.inbox + " config(s) from ~/Downloads"
    iconText: "󰋺"
    bordered: true
    selected: true
    foreground: Theme.foreground
    fontFamily: Theme.fontFamily
    onClicked: acc.vpn.run("import", ["import"])
  }

  // ---- Surfshark
  Section { title: "SURFSHARK" }
  RowLayout {
    width: parent.width
    spacing: Style.space(10)
    Text {
      text: acc.vpn && acc.vpn.surfsharkReady ? "✓" : "!"
      color: acc.vpn && acc.vpn.surfsharkReady ? Theme.foreground : Theme.urgent
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.body
      font.bold: true
    }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text {
        text: acc.vpn && acc.vpn.surfsharkReady ? "WireGuard key installed" : "Not set up yet"
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.bodySmall
        font.bold: true
      }
      Text {
        Layout.fillWidth: true
        text: acc.st ? acc.st.setup.surfsharkLocations + " locations available with one key" : ""
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
  }
  Line {
    visible: !!acc.vpn && !acc.vpn.surfsharkReady
    small: true
    tone: "dim"
    text: "1. Open my.surfshark.com → VPN → Manual setup → Desktop or mobile → WireGuard.\n2. “I don't have a key pair” → name it → Generate.\n3. Choose any location and download the .conf.\n4. Import it here. That one key unlocks every location in the picker."
  }
  ActionRow {
    icon: "󰖟"
    label: acc.vpn && acc.vpn.surfsharkReady ? "Manage keys on my.surfshark.com" : "Get the WireGuard config"
    hint: "my.surfshark.com → VPN → Manual setup"
    onActivated: acc.vpn.openUrl(acc.st.web.surfshark)
  }
  ActionRow {
    visible: !!acc.st && acc.st.apps.surfshark.installed
    icon: "󰏌"
    label: "Open the Surfshark app" + (acc.st && acc.st.apps.surfshark.running ? " (running)" : "")
    hint: "Kill switch, CleanWeb, MultiHop, Nexus · sign in once"
    onActivated: acc.vpn.openApp("surfshark")
  }
  ActionRow {
    visible: !!acc.vpn && acc.vpn.surfsharkReady
    icon: "󰆴"
    label: "Forget the Surfshark key"
    hint: "Removes the key and its profile from this PC (revoke it on the website too)"
    onActivated: acc.vpn.run("forget", ["forget-surfshark"])
  }

  // ---- Proton
  Section { title: "PROTON VPN FREE" }
  Line {
    visible: !!acc.vpn && acc.vpn.protonServers.length === 0
    small: true
    tone: "dim"
    text: "1. Create a free account at proton.me/vpn.\n2. account.protonvpn.com → Downloads → WireGuard configuration.\n3. Pick a free server (NL, JP, US, RO, PL, CA, NO, SG, CH, MX), download the .conf; repeat for others.\n4. Import them here. Free plan: one device at a time."
  }
  Repeater {
    model: acc.vpn ? acc.vpn.protonServers : []
    RowLayout {
      required property var modelData
      width: acc.width
      spacing: Style.space(10)
      Flag { cc: modelData.cc; size: 20 }
      ColumnLayout {
        Layout.fillWidth: true
        spacing: 0
        Text { text: modelData.country; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
        Text { text: modelData.city + " · " + modelData.name; color: Theme.dim; font.family: Theme.fontFamily; font.pixelSize: Style.font.caption }
      }
      PanelActionButton {
        iconText: "󰆴"
        tooltipText: "Remove this server"
        foreground: Theme.foreground
        fontFamily: Theme.fontFamily
        onClicked: acc.vpn.run("delete", ["delete", modelData.id])
      }
    }
  }
  ActionRow {
    icon: "󰖟"
    label: "Get WireGuard configs"
    hint: "account.protonvpn.com/downloads"
    onActivated: acc.vpn.openUrl(acc.st.web.proton)
  }
  ActionRow {
    visible: !!acc.st && acc.st.apps.proton.installed
    icon: "󰏌"
    label: "Open the Proton VPN app" + (acc.st && acc.st.apps.proton.running ? " (running)" : "")
    hint: "Kill switch, NetShield, automatic free server"
    onActivated: acc.vpn.openApp("protonvpn-app")
  }

  // ---- others
  Section { visible: !!acc.st && acc.st.other.length > 0; title: "OTHER VPN CONNECTIONS" }
  Repeater {
    model: acc.st ? acc.st.other : []
    Pair {
      required property var modelData
      label: modelData.name
      value: modelData.active ? "connected" : modelData.type
      strong: modelData.active
    }
  }
  Line { small: true; tone: "dim"; text: "One VPN at a time. Don't turn on the kill switch in both official apps." }
}
