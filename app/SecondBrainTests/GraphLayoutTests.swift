import Foundation
import XCTest
@testable import SecondBrain

final class GraphLayoutTests: XCTestCase {
    func testLayoutIsDeterministicForTheSameSeed() {
        let ids: [Int64] = [1, 2, 3, 4, 5, 6]
        let links = [
            GraphLink(fromID: 1, toID: 2, weight: 0.9),
            GraphLink(fromID: 2, toID: 3, weight: 0.4),
            GraphLink(fromID: 4, toID: 5, weight: 0.7),
        ]

        let first = GraphLayout.compute(nodeIDs: ids, edges: links, iterations: 80)
        let second = GraphLayout.compute(nodeIDs: ids, edges: links, iterations: 80)

        XCTAssertEqual(first, second)
    }

    func testEdgesPullLinkedNodesCloser() {
        let ids: [Int64] = [10, 20, 30]
        let withoutEdge = GraphLayout.compute(nodeIDs: ids, edges: [], iterations: 200)
        let withEdge = GraphLayout.compute(
            nodeIDs: ids,
            edges: [GraphLink(fromID: 10, toID: 20, weight: 1)],
            iterations: 200
        )

        let freeDistance = distance(withoutEdge[10], withoutEdge[20])
        let linkedDistance = distance(withEdge[10], withEdge[20])

        XCTAssertLessThan(linkedDistance, freeDistance)
    }

    func testPositionsStayInsideTheUnitSquareAndAreFinite() {
        let ids = (1...40).map(Int64.init)
        let links = ids.dropLast().map {
            GraphLink(fromID: $0, toID: $0 + 1, weight: 0.5)
        }

        let layout = GraphLayout.compute(nodeIDs: ids, edges: links, iterations: 60)

        XCTAssertEqual(layout.count, ids.count)
        for id in ids {
            let point = layout[id]
            XCTAssertNotNil(point)
            guard let point else { continue }
            XCTAssertTrue(point.x.isFinite)
            XCTAssertTrue(point.y.isFinite)
            XCTAssertGreaterThanOrEqual(point.x, 0)
            XCTAssertLessThanOrEqual(point.x, 1)
            XCTAssertGreaterThanOrEqual(point.y, 0)
            XCTAssertLessThanOrEqual(point.y, 1)
        }
    }

    func testEmptyAndSingleNodeGraphs() {
        XCTAssertTrue(GraphLayout.compute(nodeIDs: [], edges: []).isEmpty)
        XCTAssertEqual(
            GraphLayout.compute(nodeIDs: [7], edges: []),
            [7: GraphPoint(x: 0.5, y: 0.5)]
        )
    }

    func testLargeGraphReturnsAPositionForEveryNode() {
        let ids = (1...400).map(Int64.init)
        let links = (0..<399).map { index in
            GraphLink(fromID: ids[index], toID: ids[index + 1], weight: 0.3)
        }

        let layout = GraphLayout.compute(nodeIDs: ids, edges: links)

        XCTAssertEqual(layout.count, 400)
        XCTAssertTrue(layout.values.allSatisfy { $0.x.isFinite && $0.y.isFinite })
    }

    private func distance(_ lhs: GraphPoint?, _ rhs: GraphPoint?) -> Double {
        guard let lhs, let rhs else { return .infinity }
        return ((lhs.x - rhs.x) * (lhs.x - rhs.x) + (lhs.y - rhs.y) * (lhs.y - rhs.y))
            .squareRoot()
    }
}
