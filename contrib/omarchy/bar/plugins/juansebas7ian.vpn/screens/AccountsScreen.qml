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
  readonly property var key: vpn ? vpn.ssKey : null

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

  // ---- Surfshark: key made here (recommended) or taken from a downloaded .conf
  Section { title: "SURFSHARK" }
  RowLayout {
    width: parent.width
    spacing: Style.space(10)
    Text {
      text: acc.key && acc.key.verified ? "✓" : acc.key ? "2" : "1"
      color: acc.key && acc.key.verified ? Theme.foreground : Theme.urgent
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.body
      font.bold: true
    }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text {
        text: !acc.key ? "Step 1 · Create your WireGuard key"
          : !acc.key.verified ? "Step 2 · Register the public key"
          : "Key working"
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.bodySmall
        font.bold: true
      }
      Text {
        Layout.fillWidth: true
        text: !acc.key ? "Made on this PC: the private key never leaves it"
          : !acc.key.verified ? "Surfshark must know it before the first connection"
          : (acc.key.source === "generated" ? "Created here" : "From a downloaded .conf") + (acc.key.created ? " " + Theme.ago(acc.key.created) : "")
            + " · " + (acc.st ? acc.st.setup.surfsharkLocations : "") + " locations"
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
      }
    }
  }

  // Step 1
  Button {
    visible: !acc.key
    width: parent.width
    text: acc.vpn && acc.vpn.busy === "keygen" ? "Creating…" : "Create the key on this PC"
    iconText: "󰌆"
    bordered: true
    selected: true
    foreground: Theme.foreground
    fontFamily: Theme.fontFamily
    onClicked: acc.vpn.run("keygen", ["surfshark-keygen"])
  }
  Line {
    visible: !acc.key
    small: true
    tone: "dim"
    text: "Or download any .conf at my.surfshark.com → VPN → Manual setup → WireGuard and import it from ~/Downloads (above)."
  }

  // Public key (step 2 and afterwards)
  Rectangle {
    visible: !!acc.key && acc.key.publicKey !== ""
    width: parent.width
    implicitHeight: keyCol.implicitHeight + Style.space(16)
    radius: Style.cornerRadius
    color: Qt.alpha(Theme.foreground, 0.06)
    border.width: 1
    border.color: Qt.alpha(Theme.foreground, acc.key && !acc.key.verified ? 0.4 : 0.15)
    ColumnLayout {
      id: keyCol
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.margins: Style.space(10)
      spacing: Style.space(6)
      Text { text: "PUBLIC KEY"; color: Theme.dim; font.family: Theme.fontFamily; font.pixelSize: Style.font.caption; font.bold: true }
      TextEdit {
        Layout.fillWidth: true
        text: acc.key ? acc.key.publicKey : ""
        readOnly: true
        selectByMouse: true
        wrapMode: TextEdit.WrapAnywhere
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.bodySmall
      }
      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(6)
        Button {
          Layout.fillWidth: true
          text: "Copy"
          iconText: "󰆏"
          bordered: true
          foreground: Theme.foreground
          fontFamily: Theme.fontFamily
          fontSize: Style.font.caption
          onClicked: acc.vpn.copy(acc.key.publicKey)
        }
        Button {
          Layout.fillWidth: true
          text: "Open Surfshark"
          iconText: "󰖟"
          bordered: true
          foreground: Theme.foreground
          fontFamily: Theme.fontFamily
          fontSize: Style.font.caption
          onClicked: acc.vpn.openUrl(acc.st.web.surfshark)
        }
      }
    }
  }
  Line {
    visible: !!acc.key && !acc.key.verified
    small: true
    tone: "dim"
    text: "At my.surfshark.com → VPN → Manual setup → Desktop or mobile → WireGuard → “I have a key pair”: name it (e.g. omarchy), paste the public key, Save. No file to download. Then test it:"
  }
  // Step 3
  Button {
    visible: !!acc.key && !acc.key.verified
    width: parent.width
    readonly property var loc: acc.vpn ? acc.vpn.testLocation() : null
    text: acc.vpn && acc.vpn.busy !== "" && acc.vpn.busy !== "import" ? "Testing…" : "Step 3 · Test: connect to " + (loc ? acc.vpn.locLabel(loc) : "a location")
    iconText: acc.vpn ? acc.vpn.glyphOn : ""
    bordered: true
    selected: true
    foreground: Theme.foreground
    fontFamily: Theme.fontFamily
    onClicked: if (loc) acc.vpn.connectTo(loc)
  }
  ActionRow {
    visible: !!acc.st && acc.st.apps.surfshark.installed
    icon: "󰏌"
    label: "Open the Surfshark app" + (acc.st && acc.st.apps.surfshark.running ? " (running)" : "")
    hint: "Kill switch, CleanWeb, MultiHop, Nexus · sign in once"
    onActivated: acc.vpn.openApp("surfshark")
  }
  ActionRow {
    visible: !!acc.key
    icon: "󰆴"
    label: "Forget the Surfshark key"
    hint: "Removes it from this PC; delete it at my.surfshark.com too"
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
