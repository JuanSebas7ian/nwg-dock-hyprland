import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import Quickshell.Services.Mpris
import qs.Commons
import qs.Ui

// Spotify without the Spotify window: now playing with cover and progress,
// transport and volume, devices (this PC plays through spotifyd), the queue,
// recently played, top tracks of the month and your playlists. Web API via
// spotify.py; MPRIS is the fallback before the account is connected.
Panel {
  id: root
  moduleName: "juansebas7ian.spotify"
  ipcTarget: "juansebas7ian.spotify"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("spotify.py")).replace("file://", "")
  readonly property string clientIdFile: Quickshell.env("HOME") + "/.config/spotify-bar/client_id"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var st: null
  property var lib: null
  property string tab: "queue"
  property real progressMs: 0
  property string actionError: ""
  readonly property bool authorized: !!st && st.authorized === true
  readonly property var tr: st && st.track ? st.track : null
  readonly property bool playing: authorized ? !!st.playing : (mpris !== null && mpris.isPlaying)

  readonly property var mpris: {
    var list = Mpris.players ? Mpris.players.values : []
    for (var i = 0; i < list.length; i++) {
      var p = list[i]
      var n = String((p.identity || "") + " " + (p.desktopEntry || "") + " " + (p.dbusName || "")).toLowerCase()
      if (n.indexOf("spotify") >= 0) return p
    }
    return null
  }

  function fmt(ms) {
    var s = Math.max(0, Math.floor(Number(ms || 0) / 1000))
    return Math.floor(s / 60) + ":" + ("0" + (s % 60)).slice(-2)
  }

  function refresh() { if (!statusProc.running) statusProc.running = true }
  function loadLibrary() { if (!libProc.running) libProc.running = true }
  function act(name, arg) {
    actionError = ""
    var cmd = ["python3", backend, name]
    if (arg !== undefined) cmd.push(String(arg))
    actionQueue.push(cmd)
    pumpActions()
  }
  property var actionQueue: []
  function pumpActions() {
    if (actionProc.running || actionQueue.length === 0) return
    actionProc.command = actionQueue.shift()
    actionProc.running = true
  }
  function toggle() {
    if (authorized) act("toggle")
    else if (mpris && mpris.canTogglePlaying) mpris.togglePlaying()
  }
  function next() {
    if (authorized) act("next")
    else if (mpris && mpris.canGoNext) mpris.next()
  }
  function previous() {
    if (authorized) act("previous")
    else if (mpris && mpris.canGoPrevious) mpris.previous()
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    refresh()
    loadLibrary()
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Timer {
    interval: root.opened ? 2000 : 15000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }
  // Smooth progress between polls.
  Timer {
    interval: 500
    running: root.opened && root.playing
    repeat: true
    onTriggered: root.progressMs += 500
  }

  Process {
    id: statusProc
    command: ["python3", root.backend, "status"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var s = JSON.parse(text)
          root.st = s
          root.progressMs = s.progress || 0
        } catch (e) {}
      }
    }
  }
  Process {
    id: libProc
    command: ["python3", root.backend, "library"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.lib = JSON.parse(text) } catch (e) {} }
    }
  }
  Process {
    id: actionProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var r = JSON.parse(text)
          if (!r.ok) root.actionError = r.error || "Spotify did not accept that"
        } catch (e) {}
        refreshSoon.restart()
        root.pumpActions()
      }
    }
  }
  Timer { id: refreshSoon; interval: 400; onTriggered: root.refresh() }

  // One-time login: reads the Client ID saved by the installer.
  Process {
    id: authProc
    command: ["bash", "-c", "id=$(cat " + root.clientIdFile + " 2>/dev/null) && exec python3 " + root.backend + " auth \"$id\""]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { root.refresh(); root.loadLibrary() }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function playpause(): void { root.toggle() }
    function next(): void { root.next() }
    function previous(): void { root.previous() }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: ""
    dimmed: !root.playing
    tooltipText: root.tr ? (root.playing ? "▶ " : "⏸ ") + root.tr.name + " — " + root.tr.artist + (root.st.device ? "  ·  " + root.st.device : "")
      : root.mpris && root.mpris.trackTitle ? (root.playing ? "▶ " : "⏸ ") + root.mpris.trackTitle + " — " + (root.mpris.trackArtist || "")
      : "Spotify"
    onPressed: function(code) {
      if (code === Qt.MiddleButton) root.toggle()
      else if (code === Qt.RightButton) root.next()
      else root.toggle_panel()
    }
    onWheelMoved: function(delta) { if (delta > 0) root.previous(); else root.next() }
  }
  function toggle_panel() { opened ? close() : open() }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(760))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onActivateRequested: root.toggle()
      onTextKey: function(t) {
        if (t === " " || t === "p") root.toggle()
        else if (t === "l" || t === "n") root.next()
        else if (t === "h" || t === "b") root.previous()
      }

      Flickable {
        id: flick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: flick.width
          spacing: Style.space(10)

          // ------------------------------------------------ connect account
          Column {
            visible: !!root.st && !root.authorized
            width: parent.width
            spacing: Style.space(6)
            Text {
              width: parent.width
              text: "Connect your Spotify account to see the queue and your lists and to play without opening Spotify."
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
            Button {
              text: authProc.running ? "Waiting for the browser…" : "Connect Spotify"
              enabled: !authProc.running
              onClicked: authProc.running = true
            }
          }

          // ------------------------------------------------ now playing
          RowLayout {
            width: parent.width
            spacing: Style.space(12)
            Rectangle {
              Layout.preferredWidth: Style.space(84)
              Layout.preferredHeight: Style.space(84)
              radius: Style.space(6)
              color: Qt.alpha(root.foreground, 0.1)
              clip: true
              Image {
                anchors.fill: parent
                source: root.tr ? root.tr.image : (root.mpris ? root.mpris.trackArtUrl || "" : "")
                fillMode: Image.PreserveAspectCrop
                asynchronous: true
              }
              Text {
                anchors.centerIn: parent
                visible: !root.tr && !(root.mpris && root.mpris.trackArtUrl)
                text: ""
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.display
              }
            }
            ColumnLayout {
              Layout.fillWidth: true
              spacing: Style.space(2)
              Text {
                Layout.fillWidth: true
                text: root.tr ? root.tr.name : root.mpris && root.mpris.trackTitle ? root.mpris.trackTitle : "Nothing playing"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.heading
                font.bold: true
                elide: Text.ElideRight
              }
              Text {
                Layout.fillWidth: true
                text: root.tr ? root.tr.artist : root.mpris ? root.mpris.trackArtist || "" : ""
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                elide: Text.ElideRight
              }
              Text {
                Layout.fillWidth: true
                text: root.tr ? root.tr.album + (root.st.device ? "  ·  󰓃 " + root.st.device : "") : ""
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                elide: Text.ElideRight
              }
            }
          }

          // progress (click to seek)
          Column {
            visible: !!root.tr
            width: parent.width
            spacing: Style.space(3)
            Item {
              width: parent.width
              height: Style.space(6)
              Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
              Rectangle {
                height: parent.height
                radius: height / 2
                color: root.foreground
                width: root.tr && root.tr.duration > 0 ? parent.width * Math.min(1, root.progressMs / root.tr.duration) : 0
              }
              MouseArea {
                anchors.fill: parent
                anchors.margins: -Style.space(4)
                cursorShape: Qt.PointingHandCursor
                onClicked: function(m) {
                  if (!root.tr) return
                  var ms = Math.round(root.tr.duration * Math.max(0, Math.min(1, m.x / width)))
                  root.progressMs = ms
                  root.act("seek", ms)
                }
              }
            }
            Item {
              width: parent.width
              implicitHeight: elapsed.implicitHeight
              Text { id: elapsed; text: root.fmt(root.progressMs); color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
              Text { anchors.right: parent.right; text: root.tr ? root.fmt(root.tr.duration) : ""; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
            }
          }

          // transport
          RowLayout {
            width: parent.width
            spacing: Style.space(4)
            Item { Layout.fillWidth: true }
            Ctl {
              icon: "󰒝"
              tip: "Shuffle"
              on: root.authorized && root.st.shuffle
              visible: root.authorized
              onClicked: root.act("shuffle", root.st.shuffle ? "false" : "true")
            }
            Ctl { icon: "󰒮"; tip: "Previous (h)"; onClicked: root.previous() }
            Ctl { icon: root.playing ? "󰏤" : "󰐊"; tip: "Play / pause (space)"; big: true; onClicked: root.toggle() }
            Ctl { icon: "󰒭"; tip: "Next (l)"; onClicked: root.next() }
            Ctl {
              icon: root.authorized && root.st.repeat === "track" ? "󰑘" : "󰑖"
              tip: "Repeat: " + (root.authorized ? root.st.repeat : "")
              on: root.authorized && root.st.repeat !== "off"
              visible: root.authorized
              onClicked: root.act("repeat", root.st.repeat === "off" ? "context" : root.st.repeat === "context" ? "track" : "off")
            }
            Item { Layout.fillWidth: true }
          }

          // volume
          RowLayout {
            visible: root.authorized && root.st.volume !== null && root.st.volume !== undefined
            width: parent.width
            spacing: Style.space(8)
            Text { text: "󰕾"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.icon }
            PanelSlider {
              Layout.fillWidth: true
              bar: root.bar
              value: root.authorized && root.st.volume !== null ? root.st.volume / 100 : 0
              onReleased: function(v) { root.act("volume", Math.round(v * 100)) }
            }
          }

          Text {
            visible: root.actionError !== "" || (!!root.st && !!root.st.error)
            width: parent.width
            text: root.actionError !== "" ? root.actionError : (root.st ? root.st.error || "" : "")
            color: root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            wrapMode: Text.WordWrap
          }

          // devices
          Flow {
            visible: root.authorized
            width: parent.width
            spacing: Style.space(4)
            Repeater {
              model: root.authorized ? root.st.devices : []
              Chip {
                required property var modelData
                label: (modelData.type === "Computer" ? "󰍹 " : modelData.type === "Smartphone" ? "󰄜 " : "󰓃 ") + modelData.name
                on: modelData.active
                onClicked: root.act("device", modelData.id)
              }
            }
            Chip {
              visible: root.authorized && !root.st.devices.some(function(d) { return d.name === root.st.localDevice })
              label: "󰍹 This PC (start spotifyd)"
              onClicked: root.act("device", "local")
            }
          }

          // tabs
          Row {
            visible: root.authorized
            spacing: Style.space(4)
            Chip { label: "Queue"; on: root.tab === "queue"; onClicked: root.tab = "queue" }
            Chip { label: "Recent"; on: root.tab === "recent"; onClicked: root.tab = "recent" }
            Chip { label: "Top this month"; on: root.tab === "top"; onClicked: root.tab = "top" }
            Chip { label: "Playlists"; on: root.tab === "playlists"; onClicked: root.tab = "playlists" }
          }

          Column {
            visible: root.authorized
            width: parent.width
            spacing: Style.space(2)

            // Queue
            Repeater {
              model: root.tab === "queue" && root.authorized ? root.st.queue : []
              Item_ { required property var modelData; entry: modelData; onPicked: root.act("play-uri", modelData.uri) }
            }
            // Recent: places first, then tracks
            Repeater {
              model: root.tab === "recent" && root.lib ? root.lib.recentContexts || [] : []
              Item_ {
                required property var modelData
                entry: ({ name: modelData.name, artist: modelData.sub, image: modelData.image })
                onPicked: root.act("play-context", modelData.uri)
              }
            }
            Repeater {
              model: root.tab === "recent" && root.lib ? root.lib.recentTracks || [] : []
              Item_ { required property var modelData; entry: modelData; onPicked: root.act("play-uri", modelData.uri) }
            }
            Repeater {
              model: root.tab === "top" && root.lib ? root.lib.top || [] : []
              Item_ { required property var modelData; entry: modelData; onPicked: root.act("play-uri", modelData.uri) }
            }
            Repeater {
              model: root.tab === "playlists" && root.lib ? root.lib.playlists || [] : []
              Item_ {
                required property var modelData
                entry: ({ name: modelData.name, artist: modelData.sub, image: modelData.image })
                onPicked: root.act("play-context", modelData.uri)
              }
            }
            Text {
              visible: root.tab === "queue" && root.authorized && root.st.queue.length === 0
              text: "The queue is empty."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
          }
        }
      }
    }
  }

  // ---------------------------------------------------------------- parts

  component Ctl: PanelActionButton {
    property string icon: ""
    property string tip: ""
    property bool on: false
    property bool big: false
    iconText: icon
    tooltipText: tip
    fontSize: big ? Style.font.display : Style.font.heading
    foreground: on ? Color.accent : root.foreground
    fontFamily: root.fontFamily
  }

  component Chip: Rectangle {
    id: chip
    property string label: ""
    property bool on: false
    signal clicked()
    radius: Style.space(4)
    color: on ? Qt.alpha(root.foreground, 0.28) : chipMouse.containsMouse ? Qt.alpha(root.foreground, 0.18) : Qt.alpha(root.foreground, 0.08)
    implicitWidth: chipText.implicitWidth + Style.space(14)
    implicitHeight: chipText.implicitHeight + Style.space(6)
    Text {
      id: chipText
      anchors.centerIn: parent
      text: chip.label
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.caption
    }
    MouseArea { id: chipMouse; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; onClicked: chip.clicked() }
  }

  component Item_: CursorSurface {
    id: row
    property var entry: ({})
    signal picked()
    width: parent ? parent.width : 0
    foreground: root.foreground
    hasCursor: rowMouse.containsMouse
    implicitHeight: Style.space(44)
    MouseArea { id: rowMouse; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; onClicked: row.picked() }
    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(6)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(8)
      Rectangle {
        Layout.preferredWidth: Style.space(34)
        Layout.preferredHeight: Style.space(34)
        radius: Style.space(3)
        color: Qt.alpha(root.foreground, 0.1)
        clip: true
        Image { anchors.fill: parent; source: row.entry.image || ""; fillMode: Image.PreserveAspectCrop; asynchronous: true }
      }
      ColumnLayout {
        Layout.fillWidth: true
        spacing: 0
        Text {
          Layout.fillWidth: true
          text: row.entry.name || ""
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          elide: Text.ElideRight
        }
        Text {
          Layout.fillWidth: true
          text: row.entry.artist || ""
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }
      Text {
        visible: !!row.entry.duration
        text: root.fmt(row.entry.duration)
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
  }
}
