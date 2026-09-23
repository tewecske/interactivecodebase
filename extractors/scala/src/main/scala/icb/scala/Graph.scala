package icb.scala

import scala.collection.mutable

/** A source range; lines and columns are 1-based, zero means unknown. */
final case class Pos(file: String, startLine: Int, startCol: Int, endLine: Int = 0, endCol: Int = 0)

final case class Node(
  id: String,
  kind: String,
  name: String,
  pkg: String = "",
  detail: String = "",
  pos: Option[Pos] = None,
  attrs: Map[String, String] = Map.empty,
)

final case class Edge(from: String, to: String, kind: String, pos: Option[Pos] = None)

/** Collects the graph: a node added twice keeps the first, an edge added
  * twice (same endpoints, kind and position) is kept once.
  */
final class Graph {
  private val nodes = mutable.LinkedHashMap.empty[String, Node]
  private val edges = mutable.LinkedHashSet.empty[Edge]

  def addNode(n: Node): Unit = if (!nodes.contains(n.id)) nodes(n.id) = n

  def addEdge(e: Edge): Unit = edges += e

  def node(id: String): Option[Node] = nodes.get(id)

  def allNodes: Seq[Node] = nodes.values.toSeq.sortBy(_.id)

  /** The edges between nodes of the graph, sorted by source, position and
    * target so the output is deterministic.
    */
  def allEdges: Seq[Edge] = {
    edges.toSeq
      .filter(e => nodes.contains(e.from) && nodes.contains(e.to))
      .sortBy(e =>
        (e.from, e.pos.map(_.file).getOrElse(""), e.pos.map(_.startLine).getOrElse(0), e.pos.map(_.startCol).getOrElse(0), e.to, e.kind)
      )
  }

  /** The graph as JSON in docs/graph.schema.json's format. */
  def toJson: String = {
    val sb = new StringBuilder
    sb.append("{\n  \"version\": 1,\n  \"nodes\": [")
    appendAll(sb, allNodes)(nodeJson)
    sb.append("],\n  \"edges\": [")
    appendAll(sb, allEdges)(edgeJson)
    sb.append("]\n}\n")
    sb.toString
  }

  private def appendAll[A](sb: StringBuilder, as: Seq[A])(f: A => String): Unit = {
    as.zipWithIndex.foreach { case (a, i) =>
      sb.append(if (i == 0) "\n    " else ",\n    ").append(f(a))
    }
    if (as.nonEmpty) sb.append("\n  ")
  }

  private def nodeJson(n: Node): String = {
    obj(
      Seq("id" -> str(n.id), "kind" -> str(n.kind), "name" -> str(n.name)) ++
        opt("package", n.pkg) ++ opt("detail", n.detail) ++
        n.pos.map("pos" -> posJson(_)) ++ attrsJson(n.attrs)
    )
  }

  private def edgeJson(e: Edge): String = {
    obj(Seq("from" -> str(e.from), "to" -> str(e.to), "kind" -> str(e.kind)) ++ e.pos.map("pos" -> posJson(_)))
  }

  private def posJson(p: Pos): String = {
    obj(
      Seq("file" -> str(p.file), "startLine" -> p.startLine.toString, "startCol" -> p.startCol.toString) ++
        (if (p.endLine > 0) Seq("endLine" -> p.endLine.toString, "endCol" -> p.endCol.toString) else Nil)
    )
  }

  private def attrsJson(attrs: Map[String, String]): Seq[(String, String)] = {
    if (attrs.isEmpty) Nil
    else Seq("attrs" -> obj(attrs.toSeq.sortBy(_._1).map((k, v) => k -> str(v))))
  }

  private def opt(key: String, value: String): Seq[(String, String)] = {
    if (value.isEmpty) Nil else Seq(key -> str(value))
  }

  private def obj(fields: Seq[(String, String)]): String = {
    fields.map((k, v) => s"${str(k)}: $v").mkString("{", ", ", "}")
  }

  private def str(s: String): String = Json.quote(s)
}

object Json {

  /** s as a JSON string literal. */
  def quote(s: String): String = {
    val sb = new StringBuilder("\"")
    s.foreach {
      case '"'          => sb.append("\\\"")
      case '\\'         => sb.append("\\\\")
      case '\n'         => sb.append("\\n")
      case '\r'         => sb.append("\\r")
      case '\t'         => sb.append("\\t")
      case c if c < ' ' => sb.append(f"\\u${c.toInt}%04x")
      case c            => sb.append(c)
    }
    sb.append('"').toString
  }
}
