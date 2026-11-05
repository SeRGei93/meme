import Foundation

/// A point in the normalized `0...1` layout space. Kept independent of
/// CoreGraphics so the layout can run off the main actor and be tested without
/// a view.
struct GraphPoint: Equatable, Sendable {
    var x: Double
    var y: Double
}

/// One undirected link used by the layout. Node ids are translated to array
/// indices inside the engine.
struct GraphLink: Equatable, Sendable {
    let fromID: Int64
    let toID: Int64
    let weight: Double
}

/// Deterministic force-directed layout.
///
/// A Fruchterman–Reingold iteration: every pair of nodes repels, linked nodes
/// attract with a force scaled by the edge weight, and a weak gravity keeps
/// disconnected components inside the canvas. Starting positions come from a
/// golden-angle spiral seeded by `seed`, so the same graph always produces the
/// same picture and screenshot tests stay stable.
enum GraphLayout {
    static let defaultSeed: UInt64 = 0x9E37_79B9_7F4A_7C15

    /// Computes normalized positions for every node. The cost is O(n²) per
    /// iteration, so the iteration count decreases with the node count; callers
    /// run this off the main actor.
    static func compute(
        nodeIDs: [Int64],
        edges: [GraphLink],
        iterations: Int? = nil,
        seed: UInt64 = defaultSeed
    ) -> [Int64: GraphPoint] {
        let count = nodeIDs.count
        guard count > 0 else { return [:] }
        guard count > 1 else {
            return [nodeIDs[0]: GraphPoint(x: 0.5, y: 0.5)]
        }

        var rng = SplitMix64(state: seed)
        var positions = startingPositions(count: count, rng: &rng)
        let indices = Dictionary(
            uniqueKeysWithValues: nodeIDs.enumerated().map { ($0.element, $0.offset) }
        )

        var links: [(Int, Int, Double)] = []
        links.reserveCapacity(edges.count)
        for edge in edges {
            guard
                let from = indices[edge.fromID],
                let to = indices[edge.toID],
                from != to
            else { continue }
            links.append((from, to, min(max(edge.weight, 0), 1)))
        }

        let area = 1.0
        let k = (area / Double(count)).squareRoot()
        let steps = iterations ?? stepCount(for: count)
        var temperature = 0.12
        let cooling = pow(0.002 / temperature, 1 / Double(max(steps, 1)))

        for _ in 0..<steps {
            var delta = Array(repeating: GraphPoint(x: 0, y: 0), count: count)

            // Repulsion between every pair.
            for i in 0..<count {
                for j in (i + 1)..<count {
                    var dx = positions[i].x - positions[j].x
                    var dy = positions[i].y - positions[j].y
                    var distance = (dx * dx + dy * dy).squareRoot()
                    if distance < 0.0001 {
                        let angle = Double((i + 1) * (j + 3)) * 0.5
                        dx = cos(angle) * 0.0001
                        dy = sin(angle) * 0.0001
                        distance = 0.0001
                    }
                    let force = k * k / distance
                    let fx = dx / distance * force
                    let fy = dy / distance * force
                    delta[i].x += fx
                    delta[i].y += fy
                    delta[j].x -= fx
                    delta[j].y -= fy
                }
            }

            // Attraction along edges.
            for (from, to, weight) in links {
                var dx = positions[from].x - positions[to].x
                var dy = positions[from].y - positions[to].y
                var distance = (dx * dx + dy * dy).squareRoot()
                if distance < 0.0001 {
                    let angle = Double((from + 2) * (to + 5)) * 0.5
                    dx = cos(angle) * 0.0001
                    dy = sin(angle) * 0.0001
                    distance = 0.0001
                }
                let force = distance * distance / k * (0.5 + weight)
                let fx = dx / distance * force
                let fy = dy / distance * force
                delta[from].x -= fx
                delta[from].y -= fy
                delta[to].x += fx
                delta[to].y += fy
            }

            // Gravity and bounded movement (simulated annealing).
            for i in 0..<count {
                delta[i].x += (0.5 - positions[i].x) * 0.35
                delta[i].y += (0.5 - positions[i].y) * 0.35
                let magnitude = (delta[i].x * delta[i].x + delta[i].y * delta[i].y).squareRoot()
                guard magnitude > 0 else { continue }
                let step = min(magnitude, temperature)
                positions[i].x = clamp(positions[i].x + delta[i].x / magnitude * step)
                positions[i].y = clamp(positions[i].y + delta[i].y / magnitude * step)
            }

            temperature *= cooling
        }

        normalize(&positions)
        return Dictionary(uniqueKeysWithValues: zip(nodeIDs, positions))
    }

    /// Iteration budget: small graphs get a better layout, large graphs stay
    /// responsive even though the work runs off the main actor.
    private static func stepCount(for nodeCount: Int) -> Int {
        switch nodeCount {
        case ...60: return 400
        case ...150: return 250
        case ...300: return 140
        default: return 90
        }
    }

    private static func startingPositions(
        count: Int,
        rng: inout SplitMix64
    ) -> [GraphPoint] {
        (0..<count).map { index in
            let angle = Double(index) * 2.399_963_229_728_653 + rng.nextUnit() * 0.08
            let radius = 0.45 * ((Double(index) + 0.5) / Double(count)).squareRoot()
                + rng.nextUnit() * 0.01
            return GraphPoint(
                x: 0.5 + radius * cos(angle),
                y: 0.5 + radius * sin(angle)
            )
        }
    }

    private static func normalize(_ positions: inout [GraphPoint]) {
        guard let first = positions.first else { return }
        var minX = first.x
        var maxX = first.x
        var minY = first.y
        var maxY = first.y
        for point in positions {
            minX = min(minX, point.x)
            maxX = max(maxX, point.x)
            minY = min(minY, point.y)
            maxY = max(maxY, point.y)
        }

        let padding = 0.08
        let target = 1 - padding * 2
        let spanX = maxX - minX
        let spanY = maxY - minY

        for index in positions.indices {
            positions[index].x = spanX < 1e-6
                ? 0.5
                : padding + (positions[index].x - minX) / spanX * target
            positions[index].y = spanY < 1e-6
                ? 0.5
                : padding + (positions[index].y - minY) / spanY * target
        }
    }

    private static func clamp(_ value: Double) -> Double {
        min(max(value, 0.02), 0.98)
    }
}

/// Small deterministic generator so a layout never depends on `Double.random`.
private struct SplitMix64 {
    var state: UInt64

    mutating func next() -> UInt64 {
        state &+= 0x9E37_79B9_7F4A_7C15
        var z = state
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        return z ^ (z >> 31)
    }

    mutating func nextUnit() -> Double {
        Double(next() >> 11) / Double(1 << 53)
    }
}
