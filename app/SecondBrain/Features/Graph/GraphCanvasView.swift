import SwiftUI

/// Edges are drawn in a single `Canvas`; nodes are small buttons on top so
/// every node is clickable and has an accessibility element. Labels are drawn
/// on the canvas (all of them for small graphs, only the selected node for
/// large ones) to keep 400 nodes readable. Hovering only shows the native
/// tooltip, so pointer movement does not invalidate the whole canvas.
struct GraphCanvasView: View {
    let nodes: [GraphVisualNode]
    let edges: [GraphVisualEdge]
    let selectedID: Int64?
    let onSelect: (Int64) -> Void

    private var diameter: CGFloat {
        switch nodes.count {
        case ...20: return 18
        case ...60: return 14
        case ...150: return 10
        case ...300: return 8
        default: return 6
        }
    }

    private var showsAllLabels: Bool { nodes.count <= 30 }

    var body: some View {
        GeometryReader { proxy in
            let size = proxy.size
            ZStack(alignment: .topLeading) {
                Canvas { context, _ in
                    drawEdges(in: &context)
                    drawLabels(in: &context, size: size)
                }
                .allowsHitTesting(false)

                ForEach(nodes) { node in
                    nodeButton(node)
                        .position(
                            x: node.point.x * size.width,
                            y: node.point.y * size.height
                        )
                }
            }
            .background(background)
        }
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .strokeBorder(.quaternary, lineWidth: 1)
        )
    }

    private var background: some View {
        ZStack {
            Color(nsColor: .textBackgroundColor).opacity(0.35)
            Canvas { context, size in
                let step: CGFloat = 28
                var x = step
                while x < size.width {
                    var y = step
                    while y < size.height {
                        let dot = CGRect(x: x, y: y, width: 1, height: 1)
                        context.fill(Path(ellipseIn: dot), with: .color(.secondary.opacity(0.12)))
                        y += step
                    }
                    x += step
                }
            }
            .allowsHitTesting(false)
        }
    }

    private func nodeButton(_ node: GraphVisualNode) -> some View {
        let color = SourceStyle.tint(for: node.source)
        let selected = selectedID == node.id
        let hit = max(diameter, 26)

        return Button {
            onSelect(node.id)
        } label: {
            Circle()
                .fill(color.gradient)
                .frame(width: diameter, height: diameter)
                .overlay(
                    Circle()
                        .strokeBorder(
                            selected ? Color.primary : Color.white.opacity(0.35),
                            lineWidth: selected ? 2.5 : 1
                        )
                )
                .shadow(
                    color: selected ? color.opacity(0.45) : .black.opacity(0.12),
                    radius: selected ? 6 : 2
                )
                .frame(width: hit, height: hit)
                .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .help(node.title)
        .accessibilityIdentifier("graph.node.\(node.id)")
        .accessibilityLabel("\(node.title), \(SourceStyle.title(for: node.source))")
        .accessibilityHint("Открыть карточку")
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private func drawEdges(in context: inout GraphicsContext) {
        let lookup = Dictionary(uniqueKeysWithValues: nodes.map { ($0.id, $0.point) })
        for edge in edges {
            guard
                let from = lookup[edge.fromID],
                let to = lookup[edge.toID]
            else { continue }

            let active = selectedID == edge.fromID || selectedID == edge.toID

            var path = Path()
            path.move(to: CGPoint(x: from.x, y: from.y))
            path.addLine(to: CGPoint(x: to.x, y: to.y))

            context.stroke(
                path,
                with: .color(
                    active
                        ? Color.accentColor.opacity(0.65)
                        : Color.secondary.opacity(0.15 + edge.weight * 0.35)
                ),
                lineWidth: active ? 1.8 : 0.6 + edge.weight * 1.2
            )
        }
    }

    private func drawLabels(in context: inout GraphicsContext, size: CGSize) {
        for node in nodes {
            let selected = selectedID == node.id
            guard showsAllLabels || selected else { continue }

            let title = node.title.count > 34
                ? String(node.title.prefix(33)) + "…"
                : node.title
            let text = Text(title)
                .font(.system(size: selected ? 11 : 9, weight: selected ? .semibold : .regular))
                .foregroundStyle(selected ? Color.primary : Color.secondary)
            context.draw(
                text,
                at: CGPoint(
                    x: node.point.x * size.width,
                    y: node.point.y * size.height + diameter + 10
                )
            )
        }
    }
}
