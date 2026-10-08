import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Ollama in the bar: installed and loaded models, disk use, and downloads.
// All Ollama access goes through ollama-ctl.py (local API, no root).
Panel {
  id: root
  moduleName: "juansebas7ian.ollama"
  ipcTarget: "juansebas7ian.ollama"
  manageIpc: false

  readonly property string ctl: String(Qt.resolvedUrl("ollama-ctl.py")).replace("file://", "")
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var info: ({ running: false, models: [], loaded: [], diskUsed: 0, diskFree: 0, diskTotal: 0, version: "" })
  property var results: []
  property string searchError: ""
  property bool searching: false
  property string confirmDelete: ""
  property string message: ""
  property bool messageIsError: false

  // Download in progress
  property string pullModel: ""
  property string pullStatus: ""
  property real pullFraction: -1
  readonly property bool pulling: pullProcess.running

  function fmtBytes(n) {
    n = Number(n) || 0
    if (n >= 1e12) return (n / 1e12).toFixed(1) + " TB"
    if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
    if (n >= 1e6) return (n / 1e6).toFixed(0) + " MB"
    return (n / 1e3).toFixed(0) + " KB"
  }

  function refresh() { if (!statusProcess.running) statusProcess.running = true }

  function runSearch(q) {
    if (searchProcess.running) return
    searching = true
    searchError = ""
    searchProcess.command = ["python3", ctl, "search", String(q || "").trim()]
    searchProcess.running = true
  }

  function startPull(name) {
    name = String(name || "").trim()
    if (name === "" || pulling) return
    pullModel = name
    pullStatus = "starting"
    pullFraction = -1
    message = ""
    pullProcess.command = ["python3", ctl, "pull", name]
    pullProcess.running = true
  }

  function deleteModel(name) {
    if (confirmDelete !== name) {
      confirmDelete = name
      confirmTimer.restart()
      return
    }
    confirmDelete = ""
    actionProcess.command = ["python3", ctl, "rm", name]
    actionProcess.running = true
  }

  function unloadModel(name) {
    actionProcess.command = ["python3", ctl, "unload", name]
    actionProcess.running = true
  }

  function runInTerminal(name) {
    if (bar) bar.run("uwsm-app -- xdg-terminal-exec ollama run " + bar.shellQuote(name))
    root.close()
  }

  function openLibrary() {
    var q = String(field.text || "").trim()
    var url = q === "" ? "https://ollama.com/search" : "https://ollama.com/search?q=" + encodeURIComponent(q)
    if (bar) bar.run("xdg-open " + bar.shellQuote(url))
    root.close()
  }

  function submitField() {
    var text = String(field.text || "").trim()
    if (text === "") return
    // "name:tag" is a download; a bare word is a search.
    if (text.indexOf(":") >= 0) startPull(text)
    else runSearch(text)
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    refresh()
    if (results.length === 0) runSearch("")
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Timer {
    interval: root.opened || root.pulling ? 4000 : 60000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  Timer {
    id: confirmTimer
    interval: 3000
    onTriggered: root.confirmDelete = ""
  }

  Process {
    id: statusProcess
    command: ["python3", root.ctl, "status"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try { root.info = JSON.parse(text) } catch (e) {}
      }
    }
  }

  Process {
    id: searchProcess
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.searching = false
        try {
          var r = JSON.parse(text)
          root.results = r.results || []
          root.searchError = r.error ? "Could not reach ollama.com" : ""
        } catch (e) {
          root.searchError = "Search failed"
        }
      }
    }
  }

  Process {
    id: pullProcess
    stdout: SplitParser {
      onRead: function(line) {
        var d
        try { d = JSON.parse(line) } catch (e) { return }
        if (d.error) {
          root.message = "Download failed: " + d.error
          root.messageIsError = true
        } else if (d.done) {
          root.message = d.model + " downloaded"
          root.messageIsError = false
          if (root.bar) root.bar.run("notify-send -a Ollama 'Model downloaded' " + root.bar.shellQuote(d.model))
        } else {
          root.pullStatus = d.status || ""
          root.pullFraction = d.total > 0 ? d.completed / d.total : -1
        }
      }
    }
    onExited: {
      root.pullModel = ""
      root.pullFraction = -1
      root.refresh()
    }
  }

  Process {
    id: actionProcess
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var r = JSON.parse(text)
          if (!r.ok) { root.message = r.error || "Action failed"; root.messageIsError = true }
        } catch (e) {}
        root.refresh()
      }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(); return "ok" }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    // BarIconButton is one icon wide; this widget also shows text (counts, °, %, country),
    // which spilled over its neighbours. Grow with the painted text instead.
    fixedWidth: vertical ? -1 : Math.max(slotSize, Math.ceil(glyphPaintedWidth) + Style.space(10))
    text: root.pulling && root.pullFraction >= 0 ? "󰧑 " + Math.round(root.pullFraction * 100) + "%" : "󰧑"
    dimmed: !root.info.running
    tooltipText: !root.info.running ? "Ollama is not running"
      : root.info.models.length + " models · " + root.fmtBytes(root.info.diskUsed)
        + (root.info.loaded.length > 0 ? " · loaded: " + root.info.loaded.map(function(m) { return m.name }).join(", ") : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.openLibrary()
      else root.toggle()
    }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(440))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(680))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.refresh()
        else if (t === "/" || t === "s") field.forceActiveFocus()
      }

      Flickable {
        id: panelFlick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: panelFlick.width
          spacing: Style.space(12)

          PanelHero {
            width: parent.width
            title: "Ollama"
            meta: root.info.running
              ? "v" + root.info.version + " · " + root.info.models.length + " models"
              : "Not running · sudo systemctl start ollama"
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconOpacity: root.info.running ? 1.0 : 0.5
            iconComponent: Component {
              Text {
                text: "󰧑"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.display
              }
            }
            trailingControl: Component {
              PanelActionButton {
                iconText: "󰑐"
                tooltipText: "Refresh (r)"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.refresh()
              }
            }
          }

          // Disk
          Column {
            width: parent.width
            spacing: Style.space(6)
            InfoPair { label: "Models on disk"; value: root.fmtBytes(root.info.diskUsed) }
            InfoPair {
              label: "Free space"
              value: root.fmtBytes(root.info.diskFree) + " of " + root.fmtBytes(root.info.diskTotal)
            }
            Meter {
              width: parent.width
              fraction: root.info.diskTotal > 0 ? 1 - root.info.diskFree / root.info.diskTotal : 0
              accent: root.info.diskUsed / Math.max(1, root.info.diskTotal)
            }
          }

          Text {
            visible: root.message !== ""
            width: parent.width
            text: root.message
            color: root.messageIsError ? root.urgent : root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          // Loaded in memory
          Column {
            visible: root.info.loaded.length > 0
            width: parent.width
            spacing: Style.space(6)
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "LOADED"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: root.info.loaded
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name
                meta: root.fmtBytes(modelData.sizeVram) + " VRAM"
                  + (modelData.size > modelData.sizeVram ? " · " + root.fmtBytes(modelData.size - modelData.sizeVram) + " RAM" : "")
                actions: [
                  { icon: "󰅖", tip: "Unload from memory", run: function() { root.unloadModel(modelData.name) } }
                ]
              }
            }
          }

          // Installed
          Column {
            width: parent.width
            spacing: Style.space(6)
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "DOWNLOADED"; foreground: root.foreground; fontFamily: root.fontFamily }
            Text {
              visible: root.info.models.length === 0
              text: root.info.running ? "No models yet. Search below." : "Start Ollama to see your models."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
            }
            Repeater {
              model: root.info.models
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name + (modelData.loaded ? "  ●" : "")
                meta: [modelData.params, modelData.quant, root.fmtBytes(modelData.size), modelData.modified]
                  .filter(function(s) { return s }).join(" · ")
                danger: root.confirmDelete === modelData.name
                // Embedding models cannot chat, so they get no terminal button.
                actions: (/embed|bert/i.test(modelData.name + " " + modelData.family) ? [] : [
                  { icon: "\uf120", tip: "Chat in a terminal", run: function() { root.runInTerminal(modelData.name) } }
                ]).concat([
                  { icon: root.confirmDelete === modelData.name ? "󰄬" : "󰆴",
                    tip: root.confirmDelete === modelData.name ? "Click again to delete" : "Delete",
                    run: function() { root.deleteModel(modelData.name) } }
                ])
              }
            }
          }

          // Download / browse
          Column {
            width: parent.width
            spacing: Style.space(8)
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "GET MODELS"; foreground: root.foreground; fontFamily: root.fontFamily }

            RowLayout {
              width: parent.width
              spacing: Style.space(6)
              TextField {
                id: field
                Layout.fillWidth: true
                foreground: root.foreground
                placeholderText: "Search, or name:tag to download"
                enabled: root.info.running
                onAccepted: root.submitField()
                Keys.onEscapePressed: keyCatcher.forceActiveFocus()
              }
              PanelActionButton {
                iconText: "\uf002"
                tooltipText: "Search the library"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.runSearch(field.text)
              }
              PanelActionButton {
                iconText: "󰇚"
                tooltipText: "Download name:tag"
                foreground: root.foreground
                fontFamily: root.fontFamily
                enabled: root.info.running && !root.pulling && String(field.text).trim() !== ""
                onClicked: root.startPull(field.text)
              }
              PanelActionButton {
                iconText: "󰖟"
                tooltipText: "Open ollama.com"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.openLibrary()
              }
            }

            Column {
              visible: root.pulling
              width: parent.width
              spacing: Style.space(4)
              Text {
                width: parent.width
                text: "Downloading " + root.pullModel + " · " + root.pullStatus
                  + (root.pullFraction >= 0 ? " · " + Math.round(root.pullFraction * 100) + "%" : "")
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                elide: Text.ElideRight
              }
              Meter { width: parent.width; fraction: Math.max(0, root.pullFraction) }
            }

            Text {
              visible: root.searching || root.searchError !== ""
              text: root.searching ? "Searching ollama.com…" : root.searchError
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }

            Repeater {
              model: root.results
              LibraryRow {
                required property var modelData
                width: parent.width
                entry: modelData
              }
            }
          }
        }
      }
    }
  }

  // ---------------------------------------------------------------- parts

  component Meter: Item {
    property real fraction: 0
    property real accent: -1
    implicitHeight: Style.space(6)
    Rectangle {
      anchors.fill: parent
      radius: height / 2
      color: Qt.alpha(root.foreground, 0.15)
    }
    Rectangle {
      width: parent.width * Math.max(0, Math.min(1, parent.fraction))
      height: parent.height
      radius: height / 2
      color: Qt.alpha(root.foreground, 0.45)
    }
    Rectangle {
      visible: parent.accent > 0
      width: Math.max(2, parent.width * Math.min(1, parent.accent))
      height: parent.height
      radius: height / 2
      color: root.foreground
    }
  }

  component ModelRow: CursorSurface {
    id: row
    property string title: ""
    property string meta: ""
    property bool danger: false
    property var actions: []
    foreground: root.foreground
    hasCursor: hover.containsMouse
    implicitHeight: rowLayout.implicitHeight + Style.spacing.rowPaddingX

    MouseArea { id: hover; anchors.fill: parent; hoverEnabled: true; acceptedButtons: Qt.NoButton }

    RowLayout {
      id: rowLayout
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(6)

      ColumnLayout {
        Layout.fillWidth: true
        spacing: Style.space(1)
        Text {
          Layout.fillWidth: true
          text: row.title
          color: row.danger ? root.urgent : root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          elide: Text.ElideRight
        }
        Text {
          Layout.fillWidth: true
          text: row.meta
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }

      Repeater {
        model: row.actions
        PanelActionButton {
          required property var modelData
          iconText: modelData.icon
          tooltipText: modelData.tip
          foreground: row.danger && modelData.icon === "󰄬" ? root.urgent : root.foreground
          fontFamily: root.fontFamily
          onClicked: modelData.run()
        }
      }
    }
  }

  component LibraryRow: CursorSurface {
    id: lib
    property var entry: ({})
    foreground: root.foreground
    hasCursor: libHover.containsMouse
    implicitHeight: libColumn.implicitHeight + Style.spacing.rowPaddingX

    MouseArea {
      id: libHover
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: { field.text = lib.entry.name; field.forceActiveFocus() }
    }

    Column {
      id: libColumn
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(3)

      Row {
        width: parent.width
        spacing: Style.space(8)
        Text {
          text: lib.entry.name
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
        }
        Text {
          text: [lib.entry.pulls ? lib.entry.pulls + " pulls" : ""].concat(lib.entry.capabilities || [])
            .filter(function(s) { return s }).join(" · ")
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          anchors.baseline: parent.children[0].baseline
        }
      }
      Text {
        width: parent.width
        text: lib.entry.description || ""
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
        maximumLineCount: 2
        elide: Text.ElideRight
      }
      // Sizes: click one to fill "name:size" into the field (download stays explicit).
      Flow {
        width: parent.width
        spacing: Style.space(4)
        Repeater {
          model: lib.entry.sizes || []
          Rectangle {
            required property var modelData
            radius: Style.space(3)
            color: chipHover.containsMouse ? Qt.alpha(root.foreground, 0.25) : Qt.alpha(root.foreground, 0.1)
            implicitWidth: chipText.implicitWidth + Style.space(10)
            implicitHeight: chipText.implicitHeight + Style.space(4)
            Text {
              id: chipText
              anchors.centerIn: parent
              text: modelData
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
            MouseArea {
              id: chipHover
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onClicked: { field.text = lib.entry.name + ":" + modelData; field.forceActiveFocus() }
            }
          }
        }
      }
    }
  }

  component InfoPair: Row {
    property string label: ""
    property string value: ""
    width: parent.width
    spacing: Style.space(8)
    Text {
      text: label
      color: root.foreground
      opacity: 0.6
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
    Item { width: Math.max(0, parent.width - parent.children[0].implicitWidth - parent.children[2].implicitWidth - parent.spacing * 2); height: 1 }
    Text {
      text: value
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
  }
}
