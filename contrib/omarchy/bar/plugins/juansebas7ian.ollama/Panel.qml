import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Local AI in the bar. llama.cpp (llama-server router, llama-router.service)
// is the default backend: a model loads when an agent asks for it and unloads
// when idle. Ollama is the other backend, and GGUF models come from Hugging
// Face. Everything goes through the Go backend omarchy-llm (no root).
Panel {
  id: root
  moduleName: "juansebas7ian.ollama"
  ipcTarget: "juansebas7ian.ollama"
  manageIpc: false

  readonly property string ctl: Quickshell.env("HOME") + "/.local/bin/omarchy-llm"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var info: null
  readonly property var llama: info ? info.llama : null
  readonly property var ollama: info ? info.ollama : null
  readonly property var cfg: info ? info.config : null
  readonly property var gpu: info && info.gpu ? info.gpu : null
  readonly property var loadedModels: llama ? (llama.models || []).filter(function(m) { return m.status === "loaded" || m.status === "loading" }) : []

  property string tab: "installed"
  // what is on disk, grouped by role (Installed tab)
  readonly property var embedModels: ollama ? (ollama.models || []).filter(function(m) { return root.isEmbed(m) }) : []
  readonly property var ollamaOnly: ollama ? (ollama.models || []).filter(function(m) {
    return !root.isEmbed(m) && !(llama && (llama.models || []).some(function(l) { return l.source === "ollama" && l.name === m.name.replace(/:latest$/, "") || l.name === m.name }))
  }) : []
  readonly property real installedBytes: (llama ? (llama.models || []).reduce(function(a, m) { return a + (m.size || 0) }, 0) : 0)
    + embedModels.reduce(function(a, m) { return a + (m.size || 0) }, 0) + ollamaOnly.reduce(function(a, m) { return a + (m.size || 0) }, 0)
  property string message: ""
  property bool messageIsError: false
  property string confirmDelete: ""

  property var olResults: []
  property var hfResults: []
  property bool searching: false
  property string searchError: ""
  property string hfRepo: ""
  property var hfFiles: []
  property bool hfLoading: false
  property bool withMMProj: true
  property string hfPick: ""          // quantization chosen to download
  // Recommended: the biggest file that still fits fully on the GPU (best quality at full speed).
  readonly property string hfRecommended: {
    var best = "", size = -1
    for (var i = 0; i < hfFiles.length; i++) {
      var f = hfFiles[i]
      if (f.fit === "gpu" && f.size > size && !/^(F16|BF16|F32)$/.test(f.quant)) { best = f.quant; size = f.size }
    }
    return best !== "" ? best : (hfFiles.length > 0 ? hfFiles[0].quant : "")
  }
  readonly property var hfPicked: hfFiles.filter(function(f) { return f.quant === hfPick })[0] || null
  property var pi: null
  readonly property string piBin: Quickshell.env("HOME") + "/.local/bin/omarchy-agent-usage-pi"

  property string pullName: ""
  property string pullStatus: ""
  property real pullFraction: -1
  readonly property bool pulling: pullProcess.running
  property string busyModel: ""

  function fmtBytes(n) {
    n = Number(n) || 0
    if (n >= 1e12) return (n / 1e12).toFixed(1) + " TB"
    if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
    if (n >= 1e6) return (n / 1e6).toFixed(0) + " MB"
    return (n / 1e3).toFixed(0) + " KB"
  }
  function isEmbed(m) { return /embed|bert/i.test(m.name + " " + (m.family || "")) }
  function fitLabel(f) { return f === "gpu" ? "fits GPU" : f === "partial" ? "GPU + RAM" : "CPU only" }
  function fitColor(f) { return f === "gpu" ? Color.accent : f === "partial" ? root.foreground : root.urgent }

  function refresh() { if (!statusProcess.running) statusProcess.running = true }
  function say(text, isError) { message = text; messageIsError = !!isError }
  // a notice disappears by itself, so an old one never looks current
  onMessageChanged: if (message !== "") messageTimer.restart()
  Timer { id: messageTimer; interval: root.messageIsError ? 20000 : 8000; onTriggered: root.message = "" }

  function act(args, okText) {
    if (actionProcess.running) return
    message = ""
    actionProcess.okText = okText || ""
    actionProcess.command = [ctl].concat(args)
    actionProcess.running = true
  }
  function loadModel(id) { busyModel = id; act(["load", id], id + " loaded on llama.cpp") }
  function unloadModel(id) { act(["unload", id], id + " unloaded") }
  function setDefault(id) { act(["set", "defaultModel", id], id + " is now the default model for agents") }
  function setBackend(b) { act(["backend", b], "Agents now use " + (b === "llama" ? "llama.cpp" : "Ollama")) }
  function deleteModel(m) {
    var key = m.source + ":" + m.id
    if (confirmDelete !== key) { confirmDelete = key; confirmTimer.restart(); return }
    confirmDelete = ""
    if (m.source === "hf") act(["hf-rm", m.id], m.name + " deleted")
    else act(["ollama-rm", m.name], m.name + " deleted")
  }

  function search(q) {
    if (searchProcess.running) return
    searching = true
    searchError = ""
    searchProcess.kind = tab
    searchProcess.command = [ctl, tab === "hf" ? "hf-search" : "ollama-search", String(q || "").trim()]
    searchProcess.running = true
  }
  function quantHint(q) {
    q = String(q || "")
    if (/^(F16|BF16|F32)$/.test(q)) return "full precision, huge"
    if (/Q8/.test(q)) return "near lossless"
    if (/Q6/.test(q)) return "excellent"
    if (/Q5/.test(q)) return "very good"
    if (/Q4_K_M|Q4_K_L|IQ4/.test(q)) return "balanced (most used)"
    if (/Q4/.test(q)) return "good"
    if (/Q3|IQ3/.test(q)) return "smaller, some loss"
    if (/Q2|IQ2|IQ1/.test(q)) return "tiny, noticeable loss"
    return ""
  }
  function downloadPicked() {
    var f = hfPicked
    if (!f) return
    startPull(["hf-pull", hfRepo, f.quant].concat(withMMProj && f.mmproj ? ["--mmproj"] : []), hfRepo.split("/")[1] + " " + f.quant)
  }
  function editFile(path) { if (bar) bar.run("omarchy-launch-editor " + Util.shellQuote(path)); root.close() }
  function terminal(cmd) { if (bar) bar.run("uwsm-app -- xdg-terminal-exec bash -lc " + Util.shellQuote(cmd)); root.close() }
  function loadPi() { if (!piProcess.running) piProcess.running = true }
  function openRepo(repo) {
    hfRepo = repo
    hfPick = ""
    hfFiles = []
    hfLoading = true
    filesProcess.command = [ctl, "hf-files", repo]
    filesProcess.running = true
  }
  function startPull(args, label) {
    if (pulling) return
    pullName = label
    pullStatus = "starting"
    pullFraction = -1
    message = ""
    pullProcess.command = [ctl].concat(args)
    pullProcess.running = true
  }
  function submitOllama(text) {
    text = String(text || "").trim()
    if (text === "") return
    // "name:tag" or "hf.co/…" is a download; anything else is a search.
    if (text.indexOf(":") >= 0 || text.indexOf("hf.co/") === 0) startPull(["ollama-pull", text], text)
    else search(text)
  }
  function openUrl(url) { if (bar) bar.run("xdg-open " + Util.shellQuote(url)); root.close() }
  function chat(m) {
    if (!bar) return
    if (m.source === "ollama-tag") bar.run("uwsm-app -- xdg-terminal-exec ollama run " + Util.shellQuote(m.name))
    else root.openUrl(root.llama ? root.llama.endpoint.replace("/v1", "") : "http://127.0.0.1:8080")
    root.close()
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    refresh()
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }
  onTabChanged: {
    if (tab === "pi") loadPi()
    if (tab === "ollama" && olResults.length === 0) search("")
    if (tab === "hf" && hfResults.length === 0) search("")
  }

  Timer {
    interval: root.opened || root.pulling ? 4000 : 60000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }
  Timer { id: confirmTimer; interval: 3000; onTriggered: root.confirmDelete = "" }

  Process {
    id: statusProcess
    command: [root.ctl, "status"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.info = JSON.parse(text) } catch (e) {} }
    }
  }
  Process {
    id: searchProcess
    property string kind: ""
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.searching = false
        try {
          var r = JSON.parse(text)
          if (searchProcess.kind === "hf") root.hfResults = r.results || []
          else root.olResults = r.results || []
          root.searchError = r.error || ""
        } catch (e) { root.searchError = "Search failed" }
      }
    }
  }
  Process {
    id: filesProcess
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.hfLoading = false
        try {
          var r = JSON.parse(text)
          root.hfFiles = r.files || []
          root.hfPick = root.hfRecommended
          if (!r.ok) root.say(r.error || "Could not list the files", true)
        } catch (e) { root.say("Could not list the files", true) }
      }
    }
  }
  Process {
    id: piProcess
    command: [root.piBin, "--details"]
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.pi = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: measureProcess
    command: [root.piBin, "--measure"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try { var r = JSON.parse(text); root.say(r.ok ? "Pi's prompt: " + r.promptTokens + " tokens per request" : r.error, !r.ok) } catch (e) {}
        root.loadPi()
      }
    }
  }
  Process {
    id: pullProcess
    stdout: SplitParser {
      onRead: function(line) {
        var d
        try { d = JSON.parse(line) } catch (e) { return }
        if (d.error || d.ok === false) {
          root.say("Download failed: " + (d.error || "unknown error"), true)
        } else if (d.done) {
          root.say(d.model + " downloaded" + (root.cfg && root.cfg.defaultBackend === "llama" ? " · ready in llama.cpp" : ""), false)
          if (root.bar) root.bar.run("notify-send -a 'Local AI' 'Model downloaded' " + Util.shellQuote(d.model))
        } else if (d.status || d.total) {
          root.pullStatus = (d.file ? d.file + " · " : "") + (d.status || "")
          root.pullFraction = d.total > 0 ? d.completed / d.total : -1
        }
      }
    }
    onExited: { root.pullName = ""; root.pullFraction = -1; root.refresh() }
  }
  Process {
    id: actionProcess
    property string okText: ""
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.busyModel = ""
        try {
          var r = JSON.parse(text.trim().split("\n").pop())
          if (r.ok === false || r.error) root.say(r.error || "Action failed", true)
          else if (actionProcess.okText !== "") root.say(actionProcess.okText, false)
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
    function tab(name: string): void { root.tab = name; root.open() }
    function repo(name: string): void { root.tab = "hf"; root.openRepo(name); root.open() }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    // Grow with the painted text (download %), see the BarIconButton trap in omarchy.md.
    fixedWidth: vertical ? -1 : Math.max(slotSize, Math.ceil(glyphPaintedWidth) + Style.space(10))
    text: root.pulling && root.pullFraction >= 0 ? "󰧑 " + Math.round(root.pullFraction * 100) + "%" : "󰧑"
    dimmed: !root.llama || (!root.llama.running && !(root.ollama && root.ollama.running))
    tooltipText: !root.info ? "Local AI"
      : (root.cfg.defaultBackend === "llama" ? "llama.cpp" : "Ollama") + " for agents"
        + (root.loadedModels.length > 0 ? " · loaded: " + root.loadedModels.map(function(m) { return m.id }).join(", ")
          : root.llama.running ? " · server idle (no GPU used)" : " · server stopped")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.openUrl("http://127.0.0.1:" + (root.cfg ? root.cfg.port : 8080))
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
    contentWidth: panel.fittedContentWidth(Style.space(470))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(720))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.refresh()
        else if (t === "1") root.tab = "installed"
        else if (t === "2") root.tab = "llama"
        else if (t === "3") root.tab = "ollama"
        else if (t === "4") root.tab = "hf"
        else if (t === "5") root.tab = "pi"
        else if (t === "6") root.tab = "settings"
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
          width: panelFlick.width - Style.space(12)
          spacing: Style.space(10)

          PanelHero {
            width: parent.width
            title: "Local AI"
            meta: !root.info ? "Loading…"
              : "Agents use " + (root.cfg.defaultBackend === "llama" ? "llama.cpp" : "Ollama")
                + (root.cfg.defaultModel ? " · default " + root.cfg.defaultModel : "")
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component { Text { text: "󰧑"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display } }
            trailingControl: Component {
              PanelActionButton { iconText: "󰑐"; tooltipText: "Refresh (r)"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.refresh() }
            }
          }

          // GPU memory
          Column {
            visible: !!root.gpu
            width: parent.width
            spacing: Style.space(4)
            InfoPair { label: root.gpu ? root.gpu.name : "GPU"; value: root.gpu ? root.fmtBytes(root.gpu.used) + " of " + root.fmtBytes(root.gpu.total) + " used" : "" }
            Meter { width: parent.width; fraction: root.gpu ? root.gpu.used / root.gpu.total : 0 }
          }

          Flow {
            width: parent.width
            spacing: Style.space(4)
            Chip { label: "Installed"; on: root.tab === "installed"; onClicked: root.tab = "installed" }
            Chip { label: "llama.cpp" + (root.cfg && root.cfg.defaultBackend === "llama" ? " ★" : ""); on: root.tab === "llama"; onClicked: root.tab = "llama" }
            Chip { label: "Ollama" + (root.cfg && root.cfg.defaultBackend === "ollama" ? " ★" : ""); on: root.tab === "ollama"; onClicked: root.tab = "ollama" }
            Chip { label: "Hugging Face"; on: root.tab === "hf"; onClicked: root.tab = "hf" }
            Chip { label: "Pi"; on: root.tab === "pi"; onClicked: root.tab = "pi" }
            Chip { label: "Settings"; on: root.tab === "settings"; onClicked: root.tab = "settings" }
          }

          Text {
            visible: root.message !== ""
            width: parent.width
            text: root.message
            color: root.messageIsError ? root.urgent : Color.accent
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          // download progress (any tab)
          Column {
            visible: root.pulling
            width: parent.width
            spacing: Style.space(4)
            Text {
              width: parent.width
              text: "Downloading " + root.pullName + " · " + root.pullStatus + (root.pullFraction >= 0 ? " · " + Math.round(root.pullFraction * 100) + "%" : "")
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              elide: Text.ElideRight
            }
            Meter { width: parent.width; fraction: Math.max(0, root.pullFraction) }
          }

          // ============================================== Installed (everything on disk, by role)
          Column {
            visible: root.tab === "installed"
            width: parent.width
            spacing: Style.space(6)
            Text {
              width: parent.width
              text: !root.info ? "" : ((root.llama ? (root.llama.models || []).length : 0) + root.embedModels.length + root.ollamaOnly.length) + " models · "
                + root.fmtBytes(root.installedBytes) + " on disk" + (root.info.diskFree ? " · " + root.fmtBytes(root.info.diskFree) + " free" : "")
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            PanelSectionHeader { text: "CHAT AND AGENTS · LLAMA.CPP"; foreground: root.foreground; fontFamily: root.fontFamily }
            Text {
              visible: !!root.llama && (root.llama.models || []).length === 0
              width: parent.width
              text: "None: get one in the Hugging Face or Ollama tab."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
            Repeater {
              model: root.llama ? root.llama.models : []
              ModelRow {
                required property var modelData
                width: parent.width
                readonly property bool isDefault: !!root.cfg && root.cfg.defaultModel === modelData.id
                title: (isDefault ? "★ " : "") + modelData.id + (modelData.status === "loaded" ? "  ●" : "")
                meta: [isDefault ? "default for agents" : "", modelData.mmproj ? "vision" : "", root.fitLabel(modelData.fit),
                       modelData.ctx ? Math.round(modelData.ctx / 1024) + "k context" : "", modelData.quant, root.fmtBytes(modelData.size),
                       modelData.source === "hf" ? "Hugging Face" : "Ollama"]
                  .filter(function(s) { return s }).join(" · ")
                actions: isDefault ? [] : [{ icon: "󰓒", tip: "Make it the default for agents (opencode, pi)", run: function() { root.setDefault(modelData.id) } }]
              }
            }
            PanelSectionHeader { text: "EMBEDDINGS · OLLAMA"; foreground: root.foreground; fontFamily: root.fontFamily }
            Text {
              visible: root.embedModels.length === 0
              width: parent.width
              text: "None (semantic search / RAG needs one, e.g. embeddinggemma)."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
            Repeater {
              model: root.embedModels
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name + (modelData.loaded ? "  ●" : "")
                meta: ["search / RAG", modelData.params, modelData.quant, root.fmtBytes(modelData.size), "http://127.0.0.1:11434/api/embed"]
                  .filter(function(s) { return s }).join(" · ")
                actions: []
              }
            }
            PanelSectionHeader { visible: root.ollamaOnly.length > 0; text: "OTHER OLLAMA MODELS"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: root.ollamaOnly
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name + (modelData.loaded ? "  ●" : "")
                meta: [modelData.params, modelData.quant, root.fmtBytes(modelData.size)].filter(function(s) { return s }).join(" · ")
                actions: []
              }
            }
            Text {
              width: parent.width
              text: "Manage them in the llama.cpp, Ollama and Hugging Face tabs."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
          }

          // ============================================== llama.cpp
          Column {
            visible: root.tab === "llama"
            width: parent.width
            spacing: Style.space(6)

            RowLayout {
              width: parent.width
              spacing: Style.space(6)
              Text {
                Layout.fillWidth: true
                text: !root.llama ? "" : root.llama.running
                  ? "Server running · " + root.llama.endpoint + (root.loadedModels.length === 0 ? " · idle, no GPU used" : "")
                  : "Server stopped · it starts by itself when an agent needs it"
                color: root.llama && root.llama.running ? root.foreground : root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                wrapMode: Text.WordWrap
              }
              PanelActionButton {
                visible: !!root.llama && root.llama.pendingRestart
                iconText: "󰑓"
                tooltipText: "Apply new models/settings (restarts the server, unloads the model)"
                foreground: Color.accent
                fontFamily: root.fontFamily
                onClicked: root.act(["restart"], "llama.cpp server restarted")
              }
              PanelActionButton {
                iconText: root.llama && root.llama.running ? "󰓛" : "󰐊"
                tooltipText: root.llama && root.llama.running ? "Stop the server" : "Start the server and load the default model"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.llama && root.llama.running ? root.act(["stop"], "llama.cpp server stopped") : root.act(["up"], "llama.cpp ready")
              }
              PanelActionButton {
                visible: !!root.llama && root.llama.running
                iconText: "󰖟"
                tooltipText: "Open llama.cpp's chat in the browser"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.openUrl(root.llama.endpoint.replace("/v1", ""))
              }
            }

            Text {
              visible: !!root.llama && (root.llama.models || []).length === 0
              width: parent.width
              text: "No models yet: get one in the Hugging Face or Ollama tab."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              wrapMode: Text.WordWrap
            }
            Repeater {
              model: root.llama ? root.llama.models : []
              ModelRow {
                required property var modelData
                width: parent.width
                readonly property bool isDefault: !!root.cfg && root.cfg.defaultModel === modelData.id
                readonly property bool isLoaded: modelData.status === "loaded"
                title: (isDefault ? "★ " : "") + modelData.id + (isLoaded ? "  ●" : modelData.status === "loading" || root.busyModel === modelData.id ? "  …" : "")
                meta: [modelData.source === "hf" ? "Hugging Face" : "Ollama", modelData.arch, modelData.quant, root.fmtBytes(modelData.size),
                       root.fitLabel(modelData.fit), modelData.mmproj ? "vision" : "", modelData.status === "failed" ? "FAILED to load" : ""]
                  .filter(function(s) { return s }).join(" · ")
                danger: root.confirmDelete === modelData.source + ":" + modelData.id || modelData.status === "failed"
                actions: [
                  isLoaded ? { icon: "󰅖", tip: "Unload (free the GPU)", run: function() { root.unloadModel(modelData.id) } }
                           : { icon: "󰐊", tip: "Load now", run: function() { root.loadModel(modelData.id) } },
                  { icon: isDefault ? "󰓎" : "󰓒", tip: isDefault ? "Default model for agents" : "Make it the default for agents (opencode, pi)", run: function() { root.setDefault(modelData.id) } },
                  { icon: root.confirmDelete === modelData.source + ":" + modelData.id ? "󰄬" : "󰆴",
                    tip: root.confirmDelete === modelData.source + ":" + modelData.id ? "Click again to delete the file" : "Delete", run: function() { root.deleteModel(modelData) } }
                ]
              }
            }
            Text {
              width: parent.width
              text: "Agents (opencode, pi) see every model here; the one they pick loads by itself and unloads after "
                + (root.cfg ? Math.round(root.cfg.idleSeconds / 60) : 15) + " min idle."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
          }

          // ============================================== Ollama
          Column {
            visible: root.tab === "ollama"
            width: parent.width
            spacing: Style.space(6)
            Text {
              width: parent.width
              text: !root.ollama ? "" : root.ollama.running ? "Ollama " + root.ollama.version + " · " + root.ollama.models.length + " models (they also appear in llama.cpp)"
                : (root.ollama.error || "Ollama is not running")
              color: root.ollama && root.ollama.running ? root.foreground : root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            Repeater {
              model: root.ollama ? root.ollama.models : []
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name + (modelData.loaded ? "  ●" : "")
                meta: [modelData.params, modelData.quant, root.fmtBytes(modelData.size), modelData.loaded ? root.fmtBytes(modelData.vram) + " VRAM" : "", modelData.modified]
                  .filter(function(s) { return s }).join(" · ")
                danger: root.confirmDelete === "ollama:" + modelData.name
                actions: (modelData.loaded ? [{ icon: "󰅖", tip: "Unload from memory", run: function() { root.act(["ollama-unload", modelData.name], modelData.name + " unloaded") } }] : [])
                  .concat(/embed|bert/i.test(modelData.name + " " + modelData.family) ? [] : [
                    { icon: "", tip: "Chat in a terminal (Ollama)", run: function() { root.chat({ source: "ollama-tag", name: modelData.name }) } }])
                  .concat([{ icon: root.confirmDelete === "ollama:" + modelData.name ? "󰄬" : "󰆴",
                             tip: root.confirmDelete === "ollama:" + modelData.name ? "Click again to delete" : "Delete",
                             run: function() { root.deleteModel({ source: "ollama", id: modelData.name, name: modelData.name }) } }])
              }
            }
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "GET MODELS FROM OLLAMA.COM"; foreground: root.foreground; fontFamily: root.fontFamily }
            RowLayout {
              width: parent.width
              spacing: Style.space(6)
              TextField {
                id: olField
                Layout.fillWidth: true
                foreground: root.foreground
                placeholderText: "Search, or name:tag to download"
                onAccepted: root.submitOllama(text)
                Keys.onEscapePressed: keyCatcher.forceActiveFocus()
              }
              PanelActionButton { iconText: ""; tooltipText: "Search ollama.com"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.search(olField.text) }
              PanelActionButton {
                iconText: "󰇚"
                tooltipText: "Download name:tag"
                foreground: root.foreground
                fontFamily: root.fontFamily
                enabled: !root.pulling && String(olField.text).trim() !== ""
                onClicked: root.startPull(["ollama-pull", String(olField.text).trim()], String(olField.text).trim())
              }
              PanelActionButton { iconText: "󰖟"; tooltipText: "Open ollama.com"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.openUrl("https://ollama.com/search") }
            }
            Text {
              visible: root.searching || root.searchError !== ""
              text: root.searching ? "Searching…" : root.searchError
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
            Repeater {
              model: root.olResults
              CatalogRow {
                required property var modelData
                width: parent.width
                entry: modelData
                onPicked: function(size) { olField.text = modelData.name + (size ? ":" + size : ""); olField.forceActiveFocus() }
              }
            }
          }

          // ============================================== Hugging Face
          Column {
            visible: root.tab === "hf"
            width: parent.width
            spacing: Style.space(6)
            RowLayout {
              width: parent.width
              spacing: Style.space(6)
              TextField {
                id: hfField
                Layout.fillWidth: true
                foreground: root.foreground
                placeholderText: "Search GGUF models, or owner/repo"
                onAccepted: String(text).indexOf("/") > 0 ? root.openRepo(String(text).trim()) : root.search(text)
                Keys.onEscapePressed: keyCatcher.forceActiveFocus()
              }
              PanelActionButton { iconText: ""; tooltipText: "Search Hugging Face"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.search(hfField.text) }
              PanelActionButton { iconText: "󰖟"; tooltipText: "Open huggingface.co"; foreground: root.foreground; fontFamily: root.fontFamily
                onClicked: root.openUrl("https://huggingface.co/models?library=gguf&sort=trending" + (hfField.text ? "&search=" + encodeURIComponent(hfField.text) : "")) }
            }

            // files of the chosen repository
            Column {
              visible: root.hfRepo !== ""
              width: parent.width
              spacing: Style.space(4)
              RowLayout {
                width: parent.width
                PanelActionButton { iconText: "󰁍"; tooltipText: "Back to results"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: { root.hfRepo = ""; root.hfFiles = [] } }
                Text { Layout.fillWidth: true; text: root.hfRepo; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; font.bold: true; elide: Text.ElideMiddle }
                PanelActionButton { iconText: "󰖟"; tooltipText: "Open on huggingface.co"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.openUrl("https://huggingface.co/" + root.hfRepo) }
              }
              Text {
                visible: root.hfLoading
                text: "Listing files…"
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              Chip {
                visible: root.hfFiles.length > 0 && !!root.hfFiles[0].mmproj
                label: (root.withMMProj ? "☑" : "☐") + " include the vision projector (images)"
                on: root.withMMProj
                onClicked: root.withMMProj = !root.withMMProj
              }
              Text {
                visible: root.hfFiles.length > 0
                width: parent.width
                text: "Choose the quantization (fewer bits = smaller and faster, more bits = better answers):"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                wrapMode: Text.WordWrap
              }
              Rectangle {
                visible: !!root.hfPicked
                width: parent.width
                height: dlText.implicitHeight + Style.space(14)
                radius: Style.space(5)
                color: dlMouse.containsMouse ? Qt.alpha(Color.accent, 0.35) : Qt.alpha(Color.accent, 0.2)
                border.color: Color.accent
                border.width: 1
                Text {
                  id: dlText
                  anchors.centerIn: parent
                  text: root.hfPicked ? "󰇚  Download " + root.hfPicked.quant + " · " + root.fmtBytes(root.hfPicked.size)
                    + (root.withMMProj && root.hfPicked.mmproj ? " + vision" : "") : ""
                  color: root.foreground
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.body
                  font.bold: true
                }
                MouseArea { id: dlMouse; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; enabled: !root.pulling; onClicked: root.downloadPicked() }
              }
              Repeater {
                model: root.hfFiles
                QuantRow {
                  required property var modelData
                  width: parent.width
                  file: modelData
                  selected: root.hfPick === modelData.quant
                  recommended: root.hfRecommended === modelData.quant
                  onPicked: root.hfPick = modelData.quant
                }
              }
              Text {
                width: parent.width
                text: "Fits GPU = runs fully on the RTX (fast). GPU + RAM works but slower. Downloads resume if cut."
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                wrapMode: Text.WordWrap
              }
            }

            Column {
              visible: root.hfRepo === ""
              width: parent.width
              spacing: Style.space(4)
              Text {
                visible: root.searching || root.searchError !== ""
                text: root.searching ? "Searching Hugging Face…" : root.searchError
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              Repeater {
                model: root.hfResults
                CatalogRow {
                  required property var modelData
                  width: parent.width
                  entry: modelData
                  actionHint: "Choose quantization ›"
                  onPicked: function(size) { root.openRepo(modelData.name) }
                }
              }
            }
          }

          // ============================================== Pi
          Column {
            visible: root.tab === "pi"
            width: parent.width
            spacing: Style.space(6)
            Text {
              visible: !root.pi
              text: "Reading Pi's configuration…"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
            Text {
              visible: !!root.pi && !root.pi.installed
              width: parent.width
              text: "Pi is not installed (mise use -g pi)."
              color: root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
            }
            Column {
              visible: !!root.pi && root.pi.installed
              width: parent.width
              spacing: Style.space(6)
              InfoPair { label: "Pi"; value: root.pi ? "v" + root.pi.version + " · " + root.pi.backend : "" }
              InfoPair { label: "Default model"; value: root.pi ? (root.pi.defaultModel || "not set") : "" }
              InfoPair { label: "Models it can pick"; value: root.pi ? String(root.pi.models) + " (llama-local, loaded on demand)" : "" }
              InfoPair {
                label: "Prompt per request"
                value: root.pi && root.pi.promptTokens ? root.pi.promptTokens + " tokens of " + (root.pi.contextWindow || "?") : "not measured"
              }
              Text {
                visible: !!root.pi && !!root.pi.promptCost && root.pi.promptCost.total > 0
                width: parent.width
                text: root.pi && root.pi.promptCost ? "core " + root.pi.promptCost.base + " · extensions " + root.pi.promptCost.extensions + " · MCP " + root.pi.promptCost.mcp
                  + " · AGENTS.md " + root.pi.promptCost.contextFiles + " · skills " + root.pi.promptCost.skills : ""
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                wrapMode: Text.WordWrap
              }
              Row {
                spacing: Style.space(4)
                Chip { label: "󰆍 Open Pi"; onClicked: root.terminal("cd ~/src && pi") }
                Chip { label: "󰡨 Open pi-box (Docker)"; onClicked: root.terminal("cd ~/src && pi-box") }
                Chip { label: measureProcess.running ? "Measuring…" : "Measure prompt"; onClicked: if (!measureProcess.running) measureProcess.running = true }
              }

              PanelSeparator { foreground: root.foreground }
              PanelSectionHeader { text: "AGENTS.MD"; foreground: root.foreground; fontFamily: root.fontFamily }
              ModelRow {
                width: parent.width
                title: root.pi && root.pi.agentsMd ? root.pi.agentsMd.replace(Quickshell.env("HOME"), "~") : "No AGENTS.md"
                meta: root.pi ? root.pi.agentsMdWords + " words · " + (root.pi.agentsMdRules || []).join(" · ") : ""
                actions: root.pi && root.pi.agentsMd ? [{ icon: "󰏫", tip: "Edit", run: function() { root.editFile(root.pi.agentsMd) } }] : []
              }

              PanelSectionHeader { text: "MCP SERVERS"; foreground: root.foreground; fontFamily: root.fontFamily }
              Repeater {
                model: root.pi ? root.pi.mcp : []
                ModelRow {
                  required property var modelData
                  width: parent.width
                  title: (modelData.status === "connected" ? "● " : modelData.status === "failed" ? "✕ " : "○ ") + modelData.name
                  meta: [modelData.status, modelData.tools ? modelData.tools + " tools" : "", modelData.exposure, modelData.command].filter(function(s) { return s }).join(" · ")
                  danger: modelData.status === "failed"
                }
              }
              ModelRow {
                width: parent.width
                title: "~/.pi/agent/mcp.json"
                meta: "deferred = tools load only when needed (keeps the prompt small)"
                actions: [{ icon: "󰏫", tip: "Edit", run: function() { root.editFile(Quickshell.env("HOME") + "/.pi/agent/mcp.json") } }]
              }

              PanelSectionHeader { text: "EXTENSIONS"; foreground: root.foreground; fontFamily: root.fontFamily }
              Repeater {
                model: root.pi ? root.pi.extensions : []
                ModelRow {
                  required property var modelData
                  width: parent.width
                  title: modelData.name + (modelData.version ? " " + modelData.version : "") + (modelData.local ? "  (local file)" : "")
                  meta: modelData.description || modelData.spec
                }
              }
              Text {
                width: parent.width
                text: "pi-web-access (web search, PDFs, YouTube) is installed but loads only with `pi-web`: it adds ~3.2k tokens to every request."
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                wrapMode: Text.WordWrap
              }

              PanelSectionHeader { text: "SUB-AGENTS AND SKILLS"; foreground: root.foreground; fontFamily: root.fontFamily }
              Repeater {
                model: root.pi ? root.pi.subagents : []
                ModelRow { required property var modelData; width: parent.width; title: modelData.split(" (")[0]; meta: (modelData.split(" (")[1] || "").replace(")", "") }
              }
              ModelRow {
                width: parent.width
                title: "Skills"
                meta: root.pi && root.pi.skills.length > 0 ? root.pi.skills.join(", ") : "none"
              }
              ModelRow {
                width: parent.width
                title: "Sandbox"
                meta: root.pi && root.pi.sandbox ? root.pi.sandbox : "pi-box image not built yet (pi-box --build)"
              }
            }
          }

          // ============================================== Settings
          Column {
            visible: root.tab === "settings" && !!root.cfg
            width: parent.width
            spacing: Style.space(6)
            PanelSectionHeader { text: "BACKEND FOR AGENTS"; foreground: root.foreground; fontFamily: root.fontFamily }
            Row {
              spacing: Style.space(4)
              Chip { label: "llama.cpp (recommended)"; on: !!root.cfg && root.cfg.defaultBackend === "llama"; onClicked: root.setBackend("llama") }
              Chip { label: "Ollama"; on: !!root.cfg && root.cfg.defaultBackend === "ollama"; onClicked: root.setBackend("ollama") }
            }
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "LLAMA.CPP (EVERY MODEL)"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: [
                { key: "ctx", label: "Context (tokens)", hint: "32768 for agents; more context = more VRAM" },
                { key: "gpuLayers", label: "GPU layers", hint: "0 = auto: llama.cpp puts on the GPU what fits (big models spill to RAM)" },
                { key: "flashAttn", label: "Flash attention", hint: "on | off | auto" },
                { key: "cacheK", label: "KV cache K", hint: "q8_0 halves the memory of the context; f16 is exact" },
                { key: "cacheV", label: "KV cache V", hint: "q8_0 | f16 | q4_0" },
                { key: "threads", label: "CPU threads", hint: "6 = physical cores of the 9600X" },
                { key: "threadsBatch", label: "Prompt threads", hint: "12 = all threads" },
                { key: "batch", label: "Batch", hint: "2048" },
                { key: "ubatch", label: "Micro-batch", hint: "512" },
                { key: "parallel", label: "Parallel requests", hint: "1 = whole context for one agent" },
                { key: "jinja", label: "Jinja templates", hint: "on (needed for tool calls)" },
                { key: "extra", label: "Extra preset lines", hint: "key = value; key = value (llama-server options)" }
              ]
              SettingRow {
                required property var modelData
                width: parent.width
                key: modelData.key
                label: modelData.label
                hint: modelData.hint
                value: {
                  var v = root.cfg ? root.cfg.defaults[modelData.key] : undefined
                  return v === undefined || v === null ? "" : String(v === true ? "on" : v === false ? "off" : v)
                }
              }
            }
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "SERVER"; foreground: root.foreground; fontFamily: root.fontFamily }
            SettingRow { width: parent.width; key: "idleSeconds"; label: "Unload after idle (s)"; hint: "900 = 15 min; -1 = never"; value: root.cfg ? String(root.cfg.idleSeconds) : "" }
            SettingRow { width: parent.width; key: "maxLoaded"; label: "Models loaded at once"; hint: "1 on a 12 GB GPU"; value: root.cfg ? String(root.cfg.maxLoaded) : "" }
            SettingRow { width: parent.width; key: "port"; label: "Port"; hint: "8080"; value: root.cfg ? String(root.cfg.port) : "" }
            Text {
              width: parent.width
              text: "Changes rewrite ~/.config/omarchy-llm (presets, opencode, pi). The server applies them when no model is loaded, or with 󰑓 in the llama.cpp tab. Per-model: omarchy-llm set --model <id> <key> <value>."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
          }
        }
      }
    }
  }

  // ---------------------------------------------------------------- parts

  component Meter: Item {
    property real fraction: 0
    implicitHeight: Style.space(6)
    Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
    Rectangle {
      width: parent.width * Math.max(0, Math.min(1, parent.fraction))
      height: parent.height
      radius: height / 2
      color: parent.fraction > 0.9 ? root.urgent : root.foreground
    }
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
    Text { id: chipText; anchors.centerIn: parent; text: chip.label; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
    MouseArea { id: chipMouse; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; onClicked: chip.clicked() }
  }

  component ModelRow: CursorSurface {
    id: row
    property string title: ""
    property string meta: ""
    property color metaColor: root.dim
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
        Text { Layout.fillWidth: true; text: row.title; color: row.danger ? root.urgent : root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; elide: Text.ElideRight }
        Text { Layout.fillWidth: true; text: row.meta; color: row.metaColor; font.family: root.fontFamily; font.pixelSize: Style.font.caption; elide: Text.ElideRight }
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

  component CatalogRow: CursorSurface {
    id: cat
    property var entry: ({})
    property string actionHint: ""
    signal picked(string size)
    foreground: root.foreground
    hasCursor: catHover.containsMouse
    implicitHeight: catColumn.implicitHeight + Style.spacing.rowPaddingX
    MouseArea { id: catHover; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; onClicked: cat.picked("") }
    Column {
      id: catColumn
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(3)
      RowLayout {
        width: parent.width
        Text { Layout.fillWidth: true; text: cat.entry.name || ""; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; elide: Text.ElideMiddle }
        Text { visible: cat.actionHint !== ""; text: cat.actionHint; color: catHover.containsMouse ? Color.accent : root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
      }
      Text {
        width: parent.width
        text: [cat.entry.pulls ? cat.entry.pulls + (cat.entry.source === "hf" ? " downloads" : " pulls") : "", cat.entry.updated || ""]
          .concat(cat.entry.capabilities || []).concat([cat.entry.description || ""]).filter(function(s) { return s }).join(" · ")
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
        maximumLineCount: 2
        elide: Text.ElideRight
      }
      Flow {
        visible: (cat.entry.sizes || []).length > 0
        width: parent.width
        spacing: Style.space(4)
        Repeater {
          model: cat.entry.sizes || []
          Chip { required property var modelData; label: modelData; onClicked: cat.picked(modelData) }
        }
      }
    }
  }

  component QuantRow: CursorSurface {
    id: qr
    property var file: ({})
    property bool selected: false
    property bool recommended: false
    signal picked()
    foreground: root.foreground
    hasCursor: qrMouse.containsMouse || selected
    implicitHeight: qrLayout.implicitHeight + Style.spacing.rowPaddingX
    MouseArea { id: qrMouse; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; onClicked: qr.picked() }
    RowLayout {
      id: qrLayout
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(8)
      Text { text: qr.selected ? "◉" : "○"; color: qr.selected ? Color.accent : root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.body }
      ColumnLayout {
        Layout.fillWidth: true
        spacing: 0
        Text {
          Layout.fillWidth: true
          text: (qr.file.quant || "") + (qr.recommended ? "   ★ recommended" : "")
          color: qr.selected ? Color.accent : root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          font.bold: qr.selected
        }
        Text {
          Layout.fillWidth: true
          text: [root.quantHint(qr.file.quant), (qr.file.files || []).length > 1 ? (qr.file.files || []).length + " parts" : ""].filter(function(s) { return s }).join(" · ")
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
        }
      }
      Text { text: root.fmtBytes(qr.file.size); color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text { text: root.fitLabel(qr.file.fit); color: root.fitColor(qr.file.fit); font.family: root.fontFamily; font.pixelSize: Style.font.caption }
    }
  }

  component SettingRow: RowLayout {
    id: sr
    property string key: ""
    property string label: ""
    property string hint: ""
    property string value: ""
    spacing: Style.space(8)
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: sr.label; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text { Layout.fillWidth: true; text: sr.hint; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption; elide: Text.ElideRight }
    }
    TextField {
      Layout.preferredWidth: Style.space(sr.key === "extra" ? 170 : 90)
      foreground: root.foreground
      placeholderText: sr.value === "" ? "default" : sr.value
      text: ""
      onAccepted: { var v = String(text).trim(); if (v !== "") { root.act(["set", sr.key, v], sr.label + " = " + v); text = "" } }
      Keys.onEscapePressed: keyCatcher.forceActiveFocus()
    }
  }

  component InfoPair: Row {
    property string label: ""
    property string value: ""
    width: parent.width
    spacing: Style.space(8)
    Text { text: label; color: root.foreground; opacity: 0.6; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall }
    Item { width: Math.max(0, parent.width - parent.children[0].implicitWidth - parent.children[2].width - parent.spacing * 2); height: 1 }
    Text {
      width: Math.min(implicitWidth, parent.width - parent.children[0].implicitWidth - parent.spacing * 2)
      text: value
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      elide: Text.ElideMiddle
    }
  }
}
