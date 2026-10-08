import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "ui"

// VPN: Surfshark (paid) and Proton VPN Free. Every server is a WireGuard
// profile in NetworkManager; ~/.local/bin/omarchy-vpn (bar/bin/) does the
// work and prints JSON. The official apps stay available for everything the
// panel does not do (server lists, kill switch, CleanWeb, NetShield).
Panel {
  id: root
  moduleName: "juansebas7ian.vpn"
  ipcTarget: "juansebas7ian.vpn"
  manageIpc: false

  readonly property string ctl: Quickshell.env("HOME") + "/.local/bin/omarchy-vpn"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  Binding { target: Theme; property: "foreground"; value: root.foreground }
  Binding { target: Theme; property: "urgent"; value: root.urgent }
  Binding { target: Theme; property: "fontFamily"; value: root.fontFamily }

  readonly property string glyphOn: String.fromCodePoint(0xF0565)   // shield-check
  readonly property string glyphOff: String.fromCodePoint(0xF0499)  // shield-outline

  // ------------------------------------------------------------------ state
  property var st: null
  property string busy: ""        // uuid being connected, "down", "import", "toggle"
  property string message: ""
  property bool messageIsError: false

  readonly property bool connected: !!st && st.connected
  readonly property var current: connected ? st.active[0] : null
  readonly property var ip4: st && st.ip ? st.ip.ipv4 : null
  readonly property var ip6: st && st.ip ? st.ip.ipv6 : null
  readonly property bool leak: !!st && st.ipv6Leak
  readonly property var inbox: st ? (st.inbox || []) : []
  readonly property var providers: [
    { id: "surfshark", title: "SURFSHARK", app: "surfshark", appLabel: "Open the Surfshark app", appHint: "All locations, kill switch, CleanWeb, MultiHop",
      setup: "my.surfshark.com → VPN → Manual setup → Desktop → WireGuard: create a key pair, pick a location, download the .conf" },
    { id: "proton", title: "PROTON VPN FREE", app: "protonvpn-app", appLabel: "Open the Proton VPN app", appHint: "Free servers, kill switch, NetShield (sign in once)",
      setup: "account.protonvpn.com → Downloads → WireGuard configuration: pick a free server, download the .conf" }
  ]

  // ------------------------------------------------------------------ actions
  function refresh(force) {
    if (statusProc.running) return
    statusProc.command = [ctl, "status"].concat(force ? ["--refresh"] : [])
    statusProc.running = true
  }
  function run(tag, args) {
    if (actionProc.running) return
    busy = tag
    message = ""
    actionProc.command = [ctl].concat(args)
    actionProc.running = true
  }
  function connectProfile(p) { if (p.active) run("down", ["down"]); else run(p.uuid, ["up", p.uuid]) }
  function openApp(bin) { root.bar.run("uwsm-app -- " + bin); root.close() }
  function openUrl(u) { root.bar.run("xdg-open " + u); root.close() }
  function profiles(id) { return st && st.profiles ? (st.profiles[id] || []) : [] }

  onOpenedChanged: {
    if (opened) { refresh(false); Qt.callLater(function() { keyCatcher.forceActiveFocus() }) }
    else message = ""
  }

  Timer {
    interval: root.opened ? 3000 : 20000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh(false)
  }

  Process {
    id: statusProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.st = JSON.parse(text) } catch (e) {} }
    }
  }
  Process {
    id: actionProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var r = JSON.parse(text)
          if (r.ok === false) { root.message = r.error || "failed"; root.messageIsError = true }
          else if (r.imported) {
            root.message = "Imported: " + r.imported.map(function(i) { return i.name }).join(", ")
            root.messageIsError = false
          }
        } catch (e) {}
        root.busy = ""
        root.refresh(true)
      }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(true); return "ok" }
  }

  // ------------------------------------------------------------------ bar icon
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: (root.connected ? root.glyphOn : root.glyphOff) + (root.busy !== "" ? " …" : "")
    active: root.leak
    tooltipText: !root.st ? "VPN"
      : root.connected ? root.current.name + (root.ip4 ? "\n" + root.ip4.ip + " · " + (root.ip4.city ? root.ip4.city + ", " : "") + root.ip4.country : "")
        + (root.leak ? "\nIPv6 is leaking around the tunnel" : "")
      : "VPN off" + (root.ip4 ? " · " + root.ip4.country : "") + "\nRight click: connect " + (root.st.last && root.st.last.name ? root.st.last.name : "(none used yet)")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.run("toggle", ["toggle"])
      else root.toggle()
    }
  }

  // ------------------------------------------------------------------ panel
  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.fittedContentHeight(body.implicitHeight, Style.space(900))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onMoveRequested: function(dx, dy) {
        if (dy !== 0) flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + dy * Style.space(56)))
      }
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.refresh(true)
        else if (t === "d" || t === "D") { if (root.connected) root.run("down", ["down"]) }
        else if (t === "c" || t === "C") root.run("toggle", ["toggle"])
        else if (t === "i" || t === "I") root.run("import", ["import"])
      }

      Flickable {
        id: flick
        anchors.fill: parent
        contentWidth: width
        contentHeight: body.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: body
          width: flick.width - (flick.interactive ? Style.space(12) : 0)
          spacing: Style.space(8)

          PanelHero {
            width: parent.width
            title: "VPN"
            meta: !root.st ? "Checking…"
              : root.busy === "down" ? "Disconnecting…"
              : root.busy !== "" && root.busy !== "import" ? "Connecting…"
              : root.connected ? "Connected · " + root.current.name
              : "Not connected"
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component {
              Text {
                text: root.connected ? root.glyphOn : root.glyphOff
                color: root.leak ? root.urgent : root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.display
              }
            }
            trailingControl: Component {
              PanelActionButton {
                iconText: "󰑐"
                tooltipText: "Check the public IP again (r) · c = connect last / disconnect · i = import"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.refresh(true)
              }
            }
          }

          Notice { visible: root.message !== ""; text: root.message; level: root.messageIsError ? "crit" : "info" }
          Notice {
            visible: root.leak
            level: "crit"
            text: "IPv6 goes around the tunnel (" + (root.ip6 ? root.ip6.organization : "") + "). Import the server again from its .conf: imported profiles route IPv6 into the tunnel."
          }
          Notice {
            visible: root.inbox.length > 0
            level: "info"
            text: root.inbox.length + " WireGuard config(s) in ~/Downloads ready to import."
          }
          ActionRow {
            visible: root.inbox.length > 0
            icon: "󰋺"
            label: root.busy === "import" ? "Importing…" : "Import from ~/Downloads (i)"
            hint: "Each .conf becomes a server below; the file (it holds your private key) moves to ~/.config/omarchy-vpn/configs"
            onActivated: root.run("import", ["import"])
          }

          Section { title: "CONNECTION" }
          Pair { label: "Status"; value: root.connected ? "Connected" : "Off"; strong: true }
          Pair { visible: root.connected; label: "Server"; value: root.current ? root.current.name : "" }
          Pair { visible: root.connected && !!root.current.endpoint; label: "Endpoint"; value: root.current && root.current.endpoint ? root.current.endpoint : "" }
          Pair { label: "Public IP"; value: root.ip4 ? root.ip4.ip : (root.st && root.st.ip.ipv4Error ? "offline" : "…") }
          Pair { visible: !!root.ip4; label: "Seen from"; value: root.ip4 ? (root.ip4.city ? root.ip4.city + ", " : "") + root.ip4.country : "" }
          Pair { visible: !!root.ip4; label: "Network"; value: root.ip4 ? root.ip4.organization || "" : "" }
          Pair {
            label: "IPv6"
            value: !root.ip6 ? (root.connected ? "blocked (no leak)" : "not available") : root.leak ? "LEAKING · " + root.ip6.ip : root.ip6.ip
            hot: root.leak
          }
          Pair {
            visible: root.connected && !!root.current.ipv4_dns
            label: "DNS"
            value: root.current && root.current.ipv4_dns ? root.current.ipv4_dns.join(", ") + " (tunnel only)" : ""
          }
          ActionRow {
            visible: root.connected
            icon: "󰅖"
            label: root.busy === "down" ? "Disconnecting…" : "Disconnect (d)"
            hint: "Back to the normal connection"
            onActivated: root.run("down", ["down"])
          }

          Repeater {
            model: root.providers
            Column {
              id: prov
              required property var modelData
              width: body.width
              spacing: Style.space(4)
              readonly property var list: root.profiles(modelData.id)
              readonly property var app: root.st && root.st.apps ? root.st.apps[modelData.id] : null

              Section { title: prov.modelData.title }
              Repeater {
                model: prov.list
                RowLayout {
                  required property var modelData
                  width: prov.width
                  spacing: Style.space(8)
                  ColumnLayout {
                    Layout.fillWidth: true
                    spacing: 0
                    Text {
                      text: modelData.name.replace(/^(Surfshark|Proton) /, "")
                      textFormat: Text.PlainText
                      color: Theme.foreground
                      font.family: Theme.fontFamily
                      font.pixelSize: Style.font.bodySmall
                      font.bold: modelData.active
                    }
                    Text {
                      Layout.fillWidth: true
                      text: modelData.active ? "Connected" + (modelData.device ? " · " + modelData.device : "")
                        : !modelData.imported ? "Created by the official app"
                        : modelData.lastUsed > 0 ? "Last used " + Theme.ago(modelData.lastUsed) : "Never used"
                      color: Theme.dim
                      font.family: Theme.fontFamily
                      font.pixelSize: Style.font.caption
                      elide: Text.ElideRight
                    }
                  }
                  ToggleSwitch {
                    checked: modelData.active
                    busy: root.busy === modelData.uuid
                    onToggled: root.connectProfile(modelData)
                  }
                }
              }
              Line {
                visible: prov.list.length === 0
                small: true
                tone: "dim"
                text: "No servers yet. " + prov.modelData.setup + ", then import it here."
              }
              ActionRow {
                icon: "󰖟"
                label: "Get WireGuard configs"
                hint: root.st && root.st.web ? root.st.web[prov.modelData.id].replace("https://", "") : ""
                onActivated: root.openUrl(root.st.web[prov.modelData.id])
              }
              ActionRow {
                visible: !!prov.app && prov.app.installed
                icon: "󰏌"
                label: prov.modelData.appLabel + (prov.app && prov.app.running ? " (running)" : "")
                hint: prov.modelData.appHint
                onActivated: root.openApp(prov.modelData.app)
              }
            }
          }

          Section { visible: root.profiles("other").length > 0; title: "OTHER VPN" }
          Repeater {
            model: root.profiles("other")
            Pair {
              required property var modelData
              label: modelData.name
              value: modelData.active ? "connected" : modelData.type
              strong: modelData.active
            }
          }
          Line {
            small: true
            tone: "dim"
            text: "One VPN at a time: connecting a server disconnects the other. Proton Free allows one device; the Surfshark plan, unlimited."
          }
        }
      }
    }
  }
}
