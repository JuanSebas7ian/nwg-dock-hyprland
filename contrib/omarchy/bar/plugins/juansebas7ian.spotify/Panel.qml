import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import Quickshell.Services.Mpris
import qs.Commons
import qs.Ui

// Spotify without the Spotify window. Playback is spotifyd (this PC's Spotify
// Connect device "Omarchy") through MPRIS: play, pause, skip, seek, volume and
// opening any playlist/album/track need no Web API. Lists (your playlists,
// saved albums, top tracks) and other devices come from spotify-player's CLI,
// which works best with your own Spotify app (Connect → spotify-bar-setup).
Panel {
  id: root
  moduleName: "juansebas7ian.spotify"
  ipcTarget: "juansebas7ian.spotify"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("spotify.py")).replace("file://", "")
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var lib: null
  property var devs: []
  property var setup: null
  property string tab: "recent"
  property string message: ""
  property bool busy: false

  // spotifyd first (it is ours), else the Spotify app if it is open.
  readonly property var player: {
    var list = Mpris.players ? Mpris.players.values : []
    var app = null
    for (var i = 0; i < list.length; i++) {
      var p = list[i]
      var n = String((p.identity || "") + " " + (p.desktopEntry || "") + " " + (p.dbusName || "")).toLowerCase()
      if (n.indexOf("spotifyd") >= 0) return p
      if (n.indexOf("spotify") >= 0 && n.indexOf("spotify_player") < 0) app = p
    }
    return app
  }
  readonly property bool playing: !!player && player.isPlaying
  readonly property string title: player ? (player.trackTitle || "") : ""
  readonly property string artist: player ? (player.trackArtist || "") : ""
  readonly property string trackKey: player && player.metadata
    ? String(player.metadata["mpris:trackid"] || player.metadata["xesam:url"] || "") : ""
  onTrackKeyChanged: if (trackKey !== "") Quickshell.execDetached(["python3", backend, "remember", trackKey])

  readonly property var listModel: !lib ? [] : tab === "recent" ? (lib.recent || []) : tab === "albums" ? (lib.albums || [])
    : tab === "top" ? (lib.top || []).map(function(t) { return { uri: t.uri, name: t.name, sub: t.artist, image: t.image } })
    : (lib.playlists || [])

  function fmt(sec) {
    var s = Math.max(0, Math.floor(Number(sec || 0)))
    return Math.floor(s / 60) + ":" + ("0" + (s % 60)).slice(-2)
  }
  function run(args) {
    if (actionProc.running) return
    busy = true
    message = ""
    actionProc.command = ["python3", backend].concat(args)
    actionProc.running = true
  }
  // A player with a loaded track is controlled directly; otherwise start this PC.
  function toggle() {
    if (player && player.canTogglePlaying && trackKey !== "") player.togglePlaying()
    else run(["local-play"])
  }
  function next() { if (player && player.canGoNext && trackKey !== "") player.next(); else run(["local-play"]) }
  function previous() { if (player && player.canGoPrevious && trackKey !== "") player.previous(); else run(["local-play"]) }
  function playUri(uri) { run(["play-uri", uri]) }
  function connectAccount() {
    root.bar.run("omarchy-launch-floating-terminal-with-presentation spotify-bar-setup")
    root.close()
  }
  function loadLists(force) {
    if (libProc.running) return
    libProc.command = ["python3", backend, "library"].concat(force ? ["--force"] : [])
    libProc.running = true
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    setupProc.running = true
    loadLists(false)
    if (!devProc.running) devProc.running = true
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Process {
    id: actionProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.busy = false
        try {
          var r = JSON.parse(text)
          if (!r.ok) root.message = r.error || "Spotify did not accept that"
        } catch (e) {}
      }
    }
  }
  Process {
    id: libProc
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.lib = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: devProc
    command: ["python3", root.backend, "devices"]
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.devs = JSON.parse(text).devices || [] } catch (e) {} } }
  }
  Process {
    id: setupProc
    command: ["python3", root.backend, "setup-state"]
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.setup = JSON.parse(text) } catch (e) {} } }
  }
  // MPRIS position does not tick by itself.
  Timer {
    interval: 1000
    running: root.opened && root.playing
    repeat: true
    onTriggered: if (root.player) root.player.positionChanged()
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
    tooltipText: root.title !== "" ? (root.playing ? "▶ " : "⏸ ") + root.title + " — " + root.artist
      : "Spotify: click for the panel, middle-click to play on this PC"
    onPressed: function(code) {
      if (code === Qt.MiddleButton) root.toggle()
      else if (code === Qt.RightButton) root.next()
      else root.opened ? root.close() : root.open()
    }
    onWheelMoved: function(delta) { if (delta > 0) root.previous(); else root.next() }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(430))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(780))

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
        else if (t === "r") root.loadLists(true)
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
                source: root.player ? (root.player.trackArtUrl || "") : ""
                fillMode: Image.PreserveAspectCrop
                asynchronous: true
              }
              Text {
                anchors.centerIn: parent
                visible: !root.player || !root.player.trackArtUrl
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
                text: root.title !== "" ? root.title : "Nothing playing"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.heading
                font.bold: true
                elide: Text.ElideRight
              }
              Text {
                Layout.fillWidth: true
                text: root.title !== "" ? root.artist : "Press play: it starts on this PC, no Spotify window needed"
                color: root.title !== "" ? root.foreground : root.dim
                font.family: root.fontFamily
                font.pixelSize: root.title !== "" ? Style.font.body : Style.font.caption
                wrapMode: Text.WordWrap
                maximumLineCount: 2
                elide: Text.ElideRight
              }
              Text {
                Layout.fillWidth: true
                visible: !!root.player && root.title !== ""
                text: root.player ? (root.player.trackAlbum || "") + "  ·  󰓃 "
                  + (String(root.player.dbusName).indexOf("spotifyd") >= 0 ? "This PC (Omarchy)" : "Spotify app") : ""
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                elide: Text.ElideRight
              }
            }
          }

          // progress (click to seek)
          Column {
            visible: !!root.player && root.player.lengthSupported && root.player.length > 0
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
                width: root.player && root.player.length > 0 ? parent.width * Math.min(1, root.player.position / root.player.length) : 0
              }
              MouseArea {
                anchors.fill: parent
                anchors.margins: -Style.space(4)
                cursorShape: Qt.PointingHandCursor
                enabled: !!root.player && root.player.canSeek
                onClicked: function(m) { root.player.position = root.player.length * Math.max(0, Math.min(1, m.x / width)) }
              }
            }
            Item {
              width: parent.width
              implicitHeight: elapsed.implicitHeight
              Text { id: elapsed; text: root.player ? root.fmt(root.player.position) : ""; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
              Text { anchors.right: parent.right; text: root.player ? root.fmt(root.player.length) : ""; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
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
              visible: !!root.player && root.player.shuffleSupported
              on: !!root.player && root.player.shuffle
              onClicked: root.player.shuffle = !root.player.shuffle
            }
            Ctl { icon: "󰒮"; tip: "Previous (h)"; onClicked: root.previous() }
            Ctl { icon: root.busy ? "󰑐" : (root.playing ? "󰏤" : "󰐊"); tip: "Play / pause (space)"; big: true; onClicked: root.toggle() }
            Ctl { icon: "󰒭"; tip: "Next (l)"; onClicked: root.next() }
            Ctl {
              icon: root.player && root.player.loopState === MprisLoopState.Track ? "󰑘" : "󰑖"
              tip: "Repeat"
              visible: !!root.player && root.player.loopSupported
              on: !!root.player && root.player.loopState !== MprisLoopState.None
              onClicked: root.player.loopState = root.player.loopState === MprisLoopState.None ? MprisLoopState.Playlist
                : root.player.loopState === MprisLoopState.Playlist ? MprisLoopState.Track : MprisLoopState.None
            }
            Item { Layout.fillWidth: true }
          }

          RowLayout {
            visible: !!root.player && root.player.volumeSupported
            width: parent.width
            spacing: Style.space(8)
            Text { text: "󰕾"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.icon }
            PanelSlider {
              Layout.fillWidth: true
              bar: root.bar
              value: root.player ? root.player.volume : 0
              onReleased: function(v) { if (root.player) root.player.volume = v }
            }
          }

          Text {
            visible: root.message !== ""
            width: parent.width
            text: root.message
            color: root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            wrapMode: Text.WordWrap
          }

          // ------------------------------------------------ devices
          Flow {
            visible: root.devs.length > 0
            width: parent.width
            spacing: Style.space(4)
            Repeater {
              model: root.devs
              Chip {
                required property var modelData
                label: (modelData.type === "Smartphone" ? "󰄜 " : modelData.type === "Computer" ? "󰍹 " : "󰓃 ") + modelData.name
                on: !!modelData.active
                onClicked: modelData.name === "Omarchy" ? root.run(["local-play"]) : root.run(["device", modelData.name])
              }
            }
          }

          // ------------------------------------------------ connect account
          CursorSurface {
            visible: !!root.setup && (!root.setup.ownClientId || (!!root.lib && !!root.lib.error && (root.lib.playlists || []).length === 0))
            width: parent.width
            foreground: root.foreground
            hasCursor: connectMouse.containsMouse
            implicitHeight: connectCol.implicitHeight + Style.space(12)
            MouseArea { id: connectMouse; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; onClicked: root.connectAccount() }
            Column {
              id: connectCol
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              anchors.margins: Style.space(8)
              spacing: Style.space(2)
              Text {
                width: parent.width
                text: "󰌆  Connect your Spotify app"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                font.bold: true
              }
              Text {
                width: parent.width
                text: root.lib && root.lib.error ? root.lib.error + " Click to set up your own Spotify app (2 min)."
                  : "Your lists load through Spotify's shared app, which is often rate limited. Click to use your own (2 min)."
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                wrapMode: Text.WordWrap
              }
            }
          }

          // ------------------------------------------------ lists
          Row {
            spacing: Style.space(4)
            Chip { label: "Recent"; on: root.tab === "recent"; onClicked: root.tab = "recent" }
            Chip { label: "Playlists"; on: root.tab === "playlists"; onClicked: root.tab = "playlists" }
            Chip { label: "Albums"; on: root.tab === "albums"; onClicked: root.tab = "albums" }
            Chip { label: "Top this month"; on: root.tab === "top"; onClicked: root.tab = "top" }
            Chip { label: "󰑐"; onClicked: root.loadLists(true) }
          }
          Text {
            visible: root.listModel.length === 0
            width: parent.width
            text: !root.lib ? "Loading your library…" : root.lib.error ? "Lists unavailable right now." : "Nothing here yet."
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }
          Column {
            width: parent.width
            spacing: Style.space(2)
            Repeater {
              model: root.listModel
              ListRow {
                required property var modelData
                entry: modelData
                onPicked: root.playUri(modelData.uri)
              }
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

  component ListRow: CursorSurface {
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
        Text { Layout.fillWidth: true; text: row.entry.name || ""; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall; elide: Text.ElideRight }
        Text { Layout.fillWidth: true; text: row.entry.sub || ""; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption; elide: Text.ElideRight }
      }
      Text { text: "󰐊"; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.icon; visible: rowMouse.containsMouse }
    }
  }
}
