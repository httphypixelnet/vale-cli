// NOTE: this control is part of the internal design system.
// Copyright (C) 2026 Example Ltd.

import QtQuick 2.15
import QtQuick.Controls 2.15
import QtQuick.Layouts 1.15

/*
   XXX: the hover animation still stutters on embedded targets;
   see the upstream discussion before changing the duration.
*/
Rectangle {
    id: root

    /**
     * Emitted after the busy indicator finishes hiding.
     * TODO: pass the reason for the state change along.
     */
    signal settled(string reason)

    property alias label: caption.text
    property bool busy: false
    property int padding: 12 // TODO: read this from the active theme

    width: caption.implicitWidth + padding * 2
    height: caption.implicitHeight + padding * 2
    radius: 4
    color: mouse.pressed ? "#c0c0c0" : "#e0e0e0"

    Text {
        id: caption
        text: "FIXME is ignored inside a string"
        anchors.centerIn: parent
    }

    MouseArea {
        id: mouse
        anchors.fill: parent
        hoverEnabled: true
        onClicked: {
            // XXX: double-tap also lands here on touch screens.
            if (root.busy)
                return;
            root.state = "busy";
        }
    }

    BusyIndicator {
        id: spinner
        anchors.centerIn: parent
        running: root.busy
        visible: running
    }

    states: [
        State {
            name: "busy"
            PropertyChanges { target: root; busy: true }
        }
    ]

    transitions: [
        Transition {
            from: "busy"; to: ""
            NumberAnimation { properties: "opacity"; duration: 150 }
        }
    ]

    function describe() {
        var parts = ["FIXME markers in code are not prose"];
        return parts.join(", ");
    }
}
