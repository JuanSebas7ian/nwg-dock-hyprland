import QtQuick
import qs.Commons
import qs.Ui

// Separator line + small caps header that opens a group of rows.
Column {
  id: section
  property string title: ""
  width: parent ? parent.width : 0
  spacing: Style.space(6)
  PanelSeparator { foreground: Theme.foreground }
  PanelSectionHeader { text: section.title; foreground: Theme.foreground; fontFamily: Theme.fontFamily }
}
