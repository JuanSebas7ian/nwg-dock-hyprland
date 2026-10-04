import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// NVIDIA: driver and kernel module health, DKMS per kernel, CUDA (live check
// through libcuda) and pending NVIDIA/CUDA/kernel updates. The backend sends a
// desktop notification the first time it sees a new update.
Panel {
  id: root
  moduleName: "juansebas7ian.nvidia"
  ipcTarget: "juansebas7ian.nvidia"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("nvidia.py")).replace("file://", "")
  readonly property string glyph: String.fromCodePoint(0xF08AE)
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var d: null
  property bool loading: false
  readonly property var issues: d ? d.driver.issues : []
  readonly property bool cudaOk: !!d && d.cuda.works
  readonly property var pending: d && d.updates ? (d.updates.packages || []) : []
  readonly property bool problem: !!d && (issues.length > 0 || !cudaOk)

  function refresh(force) {
    if (proc.running) return
    loading = true
    proc.command = force ? ["python3", backend, "--force"] : ["python3", backend]
    proc.running = true
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    refresh(false)
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Timer { interval: 1800000; running: true; repeat: true; triggeredOnStart: true; onTriggered: root.refresh(false) }

  Process {
    id: proc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.loading = false
        try { root.d = JSON.parse(text) } catch (e) {}
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

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: root.glyph + (root.pending.length > 0 ? " " + root.pending.length : "")
    active: root.problem
    tooltipText: !root.d ? "NVIDIA"
      : "NVIDIA " + root.d.driver.loaded + " (" + root.d.driver.flavor + ") · CUDA " + (root.cudaOk ? root.d.cuda.driverCuda + " OK" : "not working")
        + (root.issues.length > 0 ? " · " + root.issues.length + " problem(s)" : "")
        + (root.pending.length > 0 ? " · " + root.pending.length + " update(s)" : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.refresh(true)
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
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(760))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) { if (t === "r" || t === "R") root.refresh(true) }

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
          spacing: Style.space(8)

          PanelHero {
            width: parent.width
            title: root.d && root.d.gpu ? "NVIDIA " + root.d.gpu.name : "NVIDIA"
            meta: !root.d ? (root.loading ? "Checking…" : "")
              : root.problem ? "Needs attention" : "Driver and CUDA OK"
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component {
              Text { text: root.glyph; color: root.problem ? root.urgent : root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
            }
            trailingControl: Component {
              PanelActionButton {
                iconText: "󰑐"
                tooltipText: "Check again, including updates (r)"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.refresh(true)
              }
            }
          }

          Repeater {
            model: root.issues
            Line { required property var modelData; text: "✖ " + modelData; color: root.urgent }
          }

          // ---------------------------------------------------------- driver
          Section { title: "DRIVER" }
          Pair { visible: !!root.d; label: "Loaded module"; value: root.d ? root.d.driver.loaded + " · " + root.d.driver.flavor : "" }
          Pair { visible: !!root.d; label: "Package"; value: root.d ? root.d.driver.modulePackage + " · utils " + root.d.driver.utils : "" }
          Repeater {
            model: root.d ? root.d.driver.kernels : []
            Pair {
              required property var modelData
              label: "DKMS · " + modelData.kernel + (modelData.running ? " (running)" : "")
              value: modelData.built ? "built ✓" : "NOT built ✖"
              hot: !modelData.built
            }
          }
          Pair { visible: !!root.d; label: "GSP firmware"; value: root.d && root.d.driver.gsp.length > 0 ? root.d.driver.gsp.length + " files ✓" : "missing"; hot: !!root.d && root.d.driver.gsp.length === 0 }
          Pair { visible: !!root.d; label: "nvidia_drm modeset"; value: root.d ? root.d.driver.modeset : "" }

          // ---------------------------------------------------------- CUDA
          Section { title: "CUDA" }
          Pair {
            visible: !!root.d
            label: "Runtime (libcuda)"
            value: !root.d ? "" : root.cudaOk ? "works ✓ · driver supports " + root.d.cuda.driverCuda : (root.d.cuda.error || "not working")
            hot: !!root.d && !root.cudaOk
          }
          Repeater {
            model: root.d && root.d.cuda.devices ? root.d.cuda.devices : []
            Pair { required property var modelData; label: modelData.name.replace("NVIDIA GeForce ", ""); value: "compute " + modelData.cc }
          }
          Pair {
            visible: !!root.d
            label: "Toolkit (cuda)"
            value: !root.d ? "" : root.d.cuda.toolkit ? root.d.cuda.toolkit + (root.d.cuda.compatible ? " ✓" : " · newer than the driver ✖") : "not installed"
            hot: !!root.d && root.d.cuda.compatible === false
          }
          Pair { visible: !!root.d; label: "cuDNN"; value: root.d && root.d.cuda.cudnn ? root.d.cuda.cudnn : "not installed" }
          Repeater {
            model: root.d ? Object.keys(root.d.cuda.extras || {}) : []
            Pair { required property var modelData; label: modelData; value: root.d.cuda.extras[modelData] }
          }

          // ---------------------------------------------------------- GPU
          Section { visible: !!root.d && !!root.d.gpu; title: "GPU" }
          Pair { visible: !!root.d && !!root.d.gpu; label: "VBIOS · PCIe"; value: root.d && root.d.gpu ? root.d.gpu.vbios + " · " + root.d.gpu.pcie : "" }
          Pair { visible: !!root.d && !!root.d.gpu; label: "Now"; value: root.d && root.d.gpu ? root.d.gpu.temp + "°C · " + Math.round(root.d.gpu.power) + " W · " + root.d.gpu.vram : "" }

          // ---------------------------------------------------------- updates
          Section { title: "UPDATES" }
          Line {
            visible: !!root.d
            text: !root.d ? "" : root.d.updates.error ? "Could not check: " + root.d.updates.error
              : root.pending.length === 0 ? "NVIDIA, CUDA and kernel are up to date." : root.pending.length + " package(s) to update:"
            color: root.pending.length > 0 ? root.foreground : root.dim
          }
          Repeater {
            model: root.pending
            Pair { required property var modelData; label: modelData.name; value: modelData.from + " → " + modelData.to }
          }
          Line {
            visible: root.pending.some(function(p) { return p.name === "linux" || p.name.indexOf("nvidia") === 0 })
            text: "After updating, omarchy-hwcheck checks that the NVIDIA module was rebuilt before you reboot."
            color: root.dim
            small: true
          }
          Button {
            visible: root.pending.length > 0
            text: "Update the system"
            onClicked: { root.bar.run("omarchy-launch-floating-terminal-with-presentation omarchy-update"); root.close() }
          }
        }
      }
    }
  }

  component Section: Column {
    property string title: ""
    width: parent.width
    spacing: Style.space(6)
    PanelSeparator { foreground: root.foreground }
    PanelSectionHeader { text: parent.title; foreground: root.foreground; fontFamily: root.fontFamily }
  }

  component Line: Text {
    property bool small: false
    width: parent ? parent.width : 0
    color: root.foreground
    font.family: root.fontFamily
    font.pixelSize: small ? Style.font.caption : Style.font.bodySmall
    wrapMode: Text.WordWrap
    textFormat: Text.PlainText
  }

  component Pair: Item {
    property string label: ""
    property string value: ""
    property bool hot: false
    width: parent ? parent.width : 0
    implicitHeight: Math.max(pl.implicitHeight, pv.implicitHeight)
    Text {
      id: pl
      anchors.left: parent.left
      anchors.right: pv.left
      anchors.rightMargin: Style.space(8)
      text: parent.label
      color: root.foreground
      opacity: 0.6
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      elide: Text.ElideRight
    }
    Text {
      id: pv
      anchors.right: parent.right
      text: parent.value
      color: parent.hot ? root.urgent : root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
  }
}
