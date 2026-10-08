import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import qs.Commons
import qs.Ui
import "../ui"

// Every app with its memory and why it is (or is not) protected, with one-click
// exceptions; and the three exception lists with an add field.
Column {
  id: tab
  property var mg: null
  spacing: Style.space(8)

  readonly property var user: mg && mg.ui && mg.ui.config ? mg.ui.config.user : ({})
  readonly property var lists: [
    { id: "protect", title: "ALWAYS PROTECT", hint: "Never paused, capped or given to zram; oomd takes it last" },
    { id: "ordinary", title: "TREAT AS ORDINARY", hint: "Built-in protection removed (never the desktop)" },
    { id: "freezable", title: "PAUSABLE BACKGROUND JOBS", hint: "User units paused under pressure, besides the built-in ones" }
  ]
  function inList(list, name) { return (tab.user[list] || []).indexOf(name) >= 0 }
  function add(list) {
    var name = String(field.text).trim()
    if (name === "") return
    tab.mg.change("add", list, name, name)
    field.text = ""
  }

  Section { title: "ADD AN EXCEPTION" }
  Line { small: true; tone: "dim"; text: "A process name (python3, java, slack), an app or unit name (app-slack, my-job.service)." }
  RowLayout {
    width: parent.width
    spacing: Style.space(6)
    TextField {
      id: field
      Layout.fillWidth: true
      foreground: Theme.foreground
      placeholderText: "name"
      onAccepted: tab.add("protect")
    }
    PanelActionButton { iconText: "󰒃"; tooltipText: "Always protect"; foreground: Theme.foreground; fontFamily: Theme.fontFamily; onClicked: tab.add("protect") }
    PanelActionButton { iconText: "󰒄"; tooltipText: "Treat as ordinary"; foreground: Theme.foreground; fontFamily: Theme.fontFamily; onClicked: tab.add("ordinary") }
    PanelActionButton { iconText: "󰏤"; tooltipText: "Pausable background job (unit)"; foreground: Theme.foreground; fontFamily: Theme.fontFamily; onClicked: tab.add("freezable") }
  }

  Repeater {
    model: tab.lists
    Column {
      id: listBox
      required property var modelData
      width: tab.width
      spacing: Style.space(4)
      readonly property var items: tab.user[modelData.id] || []
      visible: items.length > 0 || modelData.id === "freezable"
      Section { title: listBox.modelData.title }
      Line { small: true; tone: "dim"; text: listBox.modelData.hint
        + (listBox.modelData.id === "freezable" && tab.mg && tab.mg.ui ? ". Built in: " + tab.mg.ui.config.builtinFreezable.join(", ") : "") }
      Repeater {
        model: listBox.items
        RowLayout {
          required property var modelData
          width: tab.width
          Text {
            Layout.fillWidth: true
            text: modelData
            color: Theme.foreground
            font.family: Theme.fontFamily
            font.pixelSize: Style.font.bodySmall
            elide: Text.ElideRight
          }
          PanelActionButton {
            iconText: "󰅖"
            tooltipText: "Remove this exception"
            foreground: Theme.foreground
            fontFamily: Theme.fontFamily
            onClicked: tab.mg.forget(listBox.modelData.id, modelData)
          }
        }
      }
    }
  }

  Section { title: "APPS NOW" }
  Repeater {
    model: tab.mg && tab.mg.ui ? (tab.mg.ui.apps || []) : []
    RowLayout {
      id: app
      required property var modelData
      width: tab.width
      spacing: Style.space(6)
      readonly property string name: modelData.match
      ColumnLayout {
        Layout.fillWidth: true
        spacing: 0
        Text {
          Layout.fillWidth: true
          text: app.modelData.label
          color: app.modelData.capped ? Theme.urgent : Theme.foreground
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.bodySmall
          elide: Text.ElideRight
        }
        Text {
          Layout.fillWidth: true
          text: tab.mg.bytes(app.modelData.mem) + (app.modelData.swap > 0 ? " + " + tab.mg.bytes(app.modelData.swap) + " swap" : "")
            + " · " + (app.modelData.kind ? "protected (" + tab.mg.kindLabel(app.modelData.kind) + ": " + app.modelData.why + ")"
                                          : app.modelData.why || "ordinary")
            + (app.modelData.capped ? " · capped" : "")
          color: Theme.dim
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }
      PanelActionButton {
        // ordinary -> protect; protected by you -> undo; protected built-in -> make ordinary
        visible: app.modelData.kind !== "desktop"
        iconText: app.modelData.kind === "user" || tab.inList("ordinary", app.name) ? "󰕌" : app.modelData.kind ? "󰒄" : "󰒃"
        tooltipText: app.modelData.kind === "user" ? "Stop protecting " + app.name
          : tab.inList("ordinary", app.name) ? "Give back its built-in protection"
          : app.modelData.kind ? "Treat " + app.name + " as ordinary" : "Always protect " + app.name
        foreground: Theme.foreground
        fontFamily: Theme.fontFamily
        enabled: !!tab.mg && tab.mg.busy === ""
        onClicked: {
          if (app.modelData.kind === "user") tab.mg.forget("protect", app.name)
          else if (tab.inList("ordinary", app.name)) tab.mg.forget("ordinary", app.name)
          else if (app.modelData.kind) tab.mg.makeOrdinary(app.name)
          else tab.mg.protect(app.name)
        }
      }
    }
  }
}
