import QtQuick
import qs.Commons
import "../ui"

// GPU: NVIDIA live (NVML) with the processes using it, the Radeon iGPU,
// and the driver stack: module, DKMS per kernel, GSP, CUDA (live check), cuDNN.
Column {
  id: tab
  property var hw: null
  spacing: Style.space(10)

  readonly property var g: hw ? hw.gpu : null
  readonly property var nv: hw && hw.drivers ? hw.drivers.nvidia : null
  readonly property var cu: hw && hw.drivers ? hw.drivers.cuda : null
  readonly property var igpu: hw && hw.live ? hw.live.igpu : null

  Repeater {
    model: tab.hw ? tab.hw.alertsFor("gpu") : []
    Notice { required property var modelData; level: modelData.level; text: modelData.title + " · " + modelData.detail }
  }
  Line { visible: !!tab.hw && !!tab.hw.live && !tab.g; text: "No NVIDIA GPU answered through NVML."; tone: "dim" }

  // ------------------------------------------------------------ live
  Graph {
    visible: !!tab.g
    label: "Utilization · " + (tab.g ? tab.g.util + "% · P" + tab.g.pstate : "")
    values: tab.hw ? tab.hw.hist.gpu : []
  }
  Gauge {
    visible: !!tab.g
    label: "Core (SM)"
    value: tab.g ? tab.g.util + "% · " + tab.g.clockCore + " MHz" : ""
    fraction: tab.g ? tab.g.util / 100 : 0
  }
  Gauge {
    visible: !!tab.g
    label: "Memory bandwidth"
    value: tab.g ? tab.g.memUtil + "% · " + tab.g.clockMem + " MHz" : ""
    fraction: tab.g ? tab.g.memUtil / 100 : 0
  }
  Gauge {
    visible: !!tab.g
    label: "VRAM"
    value: tab.g ? Theme.gb(tab.g.vramUsed) + " / " + Theme.gb(tab.g.vramTotal) + " GB" : ""
    fraction: tab.g ? tab.g.vramUsed / tab.g.vramTotal : 0
  }
  Gauge {
    visible: !!tab.g && tab.g.power !== null
    label: "Power"
    value: tab.g && tab.g.power !== null ? Math.round(tab.g.power) + " / " + Math.round(tab.g.powerLimit) + " W" : ""
    fraction: tab.g && tab.g.power !== null ? tab.g.power / tab.g.powerLimit : 0
  }
  Gauge {
    visible: !!tab.g
    label: "Temperature · fan"
    value: tab.g ? Theme.deg(tab.g.temp) + " · fan " + (tab.g.fan === null ? "–" : tab.g.fan + "%") : ""
    fraction: tab.g ? tab.g.temp / 90 : 0
    hot: !!tab.hw && tab.hw.alertsFor("gpu").length > 0
  }
  Pair {
    visible: !!tab.g
    label: tab.g ? "PCIe Gen" + tab.g.pcieGen + " x" + tab.g.pcieWidth + " (max Gen" + tab.g.pcieGenMax + ")" : ""
    value: tab.g ? "↓ " + Theme.rate(tab.g.pcieRx * 1024) + "  ↑ " + Theme.rate(tab.g.pcieTx * 1024) : ""
  }
  Pair { visible: !!tab.g; label: "Video encoder · decoder"; value: tab.g ? tab.g.enc + "% · " + tab.g.dec + "%" : "" }

  Section { visible: !!tab.g; title: "PROCESSES ON THE GPU" }
  Line { visible: !!tab.g && tab.hw.gpuProcs.length === 0; text: "Nothing is using the GPU."; tone: "dim" }
  Repeater {
    model: tab.hw ? tab.hw.gpuProcs : []
    Pair {
      required property var modelData
      label: modelData.name + (modelData.type.indexOf("C") >= 0 ? " · compute" : "")
      value: (modelData.sm !== undefined && modelData.sm !== null ? modelData.sm + "% SM · " : "") + (modelData.mem / 1048576).toFixed(0) + " MB"
    }
  }

  // ------------------------------------------------------------ iGPU
  Section { visible: !!tab.igpu; title: "INTEGRATED · RADEON" }
  Gauge {
    visible: !!tab.igpu
    label: "Usage · temperature"
    value: tab.igpu ? (tab.igpu.util !== null ? tab.igpu.util + "%" : "–") + " · " + Theme.deg(tab.igpu.temp) : ""
    fraction: tab.igpu && tab.igpu.util !== null ? tab.igpu.util / 100 : 0
  }

  // ------------------------------------------------------------ driver
  Section { title: "DRIVER" }
  Line { visible: !tab.nv; text: "Checking the driver…"; tone: "dim" }
  Repeater {
    model: tab.nv ? tab.nv.issues : []
    Line { required property var modelData; text: "✖ " + modelData; tone: "urgent" }
  }
  Pair { visible: !!tab.nv; label: "Loaded module"; value: tab.nv ? tab.nv.loaded + " · " + tab.nv.flavor : "" }
  Pair { visible: !!tab.nv; label: "Package"; value: tab.nv ? tab.nv.modulePackage + " · utils " + tab.nv.utils : "" }
  Repeater {
    model: tab.nv ? tab.nv.kernels : []
    Pair {
      required property var modelData
      label: "DKMS · " + modelData.kernel + (modelData.running ? " (running)" : "")
      value: modelData.built ? "built ✓" : "NOT built ✖"
      hot: !modelData.built
    }
  }
  Pair { visible: !!tab.nv; label: "GSP firmware · modeset"; value: tab.nv ? (tab.nv.gsp > 0 ? tab.nv.gsp + " files ✓" : "missing") + " · " + tab.nv.modeset : ""; hot: !!tab.nv && tab.nv.gsp === 0 && tab.nv.loaded !== "" }
  Pair { visible: !!tab.nv && !!tab.nv.card; label: "VBIOS · PCIe max · persistence"; value: tab.nv && tab.nv.card ? tab.nv.card.vbios + " · " + tab.nv.card.pcie + " · " + tab.nv.card.persistence : "" }

  // ------------------------------------------------------------ CUDA
  Section { title: "CUDA" }
  Pair {
    visible: !!tab.cu
    label: "Runtime (libcuda)"
    value: !tab.cu ? "" : tab.cu.works ? "works ✓ · driver supports " + tab.cu.driverCuda : (tab.cu.error || "not working")
    hot: !!tab.cu && !tab.cu.works
  }
  Repeater {
    model: tab.cu && tab.cu.devices ? tab.cu.devices : []
    Pair { required property var modelData; label: modelData.name.replace("NVIDIA GeForce ", ""); value: "compute " + modelData.cc }
  }
  Pair {
    visible: !!tab.cu
    label: "Toolkit (cuda)"
    value: !tab.cu ? "" : tab.cu.toolkit ? tab.cu.toolkit + (tab.cu.compatible === false ? " · newer than the driver ✖" : " ✓") : "not installed"
    hot: !!tab.cu && tab.cu.compatible === false
  }
  Pair { visible: !!tab.cu; label: "cuDNN"; value: tab.cu && tab.cu.cudnn ? tab.cu.cudnn : "not installed" }
  Repeater {
    model: tab.cu ? Object.keys(tab.cu.extras || {}) : []
    Pair { required property var modelData; label: modelData; value: tab.cu.extras[modelData] }
  }
  Caption { text: "NVIDIA, CUDA and kernel updates are in the Drivers tab (a notification arrives once per new version)." }
}
