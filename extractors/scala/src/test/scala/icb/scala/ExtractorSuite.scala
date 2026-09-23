package icb.scala

import java.nio.file.{Path, Paths}

class ExtractorSuite extends munit.FunSuite {

  // The test classes directory holds the TASTy of src/test/scala/sample.
  private lazy val graph: Graph = {
    val classes = Paths.get(classOf[ExtractorSuite].getProtectionDomain.getCodeSource.getLocation.toURI)
    val classpath = System.getProperty("java.class.path").split(java.io.File.pathSeparator).toList
    Main.run(Args(Paths.get("").toAbsolutePath, None, List(Module(classpath, List(classes.resolve("sample").toString)))))
  }

  private def node(id: String): Node = graph.node(id).getOrElse(fail(s"no node $id; have ${graph.allNodes.map(_.id).mkString("\n")}"))

  private def hasEdge(from: String, to: String, kind: String): Boolean = {
    graph.allEdges.exists(e => e.from == from && e.to == to && e.kind == kind)
  }

  private def assertEdge(from: String, to: String, kind: String)(using munit.Location): Unit = {
    assert(hasEdge(from, to, kind), s"no $kind edge $from -> $to; have\n${graph.allEdges.mkString("\n")}")
  }

  test("functions and methods") {
    val put = node("method:(sample.Service).put")
    assertEquals(put.kind, "method")
    assertEquals(put.name, "Service.put")
    assertEquals(put.pkg, "sample")
    assertEquals(put.detail, "(items: List[Item]): Unit")
    assertEquals(put.pos.map(_.file), Some("src/test/scala/sample/Sample.scala"))
    assertEquals(put.pos.map(_.startLine), Some(32))
    assertEquals(node("func:sample.Service.default").kind, "func")
    assertEquals(node("func:sample.run").name, "run")
    assertEquals(node("func:sample.NullStore.save").kind, "func")
  }

  test("overloads are told apart by parameter types") {
    node("method:(sample.Service).find(Int)")
    node("method:(sample.Service).find(String)")
    assertEdge("method:(sample.Service).find(Int)", "method:(sample.Service).get", "calls")
  }

  test("compiler-generated members are left out") {
    val ids = graph.allNodes.map(_.id)
    assert(!ids.exists(_.contains("copy")), ids)
    assert(!ids.exists(_.contains("<init>")), ids)
    assert(!ids.exists(_.contains("$")), ids)
  }

  test("calls, through lambdas and local defs") {
    assertEdge("func:sample.run", "func:sample.Service.default", "calls")
    assertEdge("func:sample.run", "method:(sample.Service).put", "calls")
    assertEdge("method:(sample.Service).get", "method:(sample.Service).fallback", "calls")
    assertEdge("func:sample.NullStore.save", "method:(sample.Logging).log", "calls")
  }

  test("trait calls dispatch to every implementation") {
    val save = "interface_call:(sample.Store).save"
    assertEquals(node(save).name, "Store.save")
    assertEdge("method:(sample.Service).put", save, "calls")
    assertEdge(save, "method:(sample.MemStore).save", "dispatches_to")
    assertEdge(save, "func:sample.NullStore.save", "dispatches_to")
    assertEdge("method:(sample.Service).get", "interface_call:(sample.Store).load", "calls")
  }

  test("types, implements and uses_type") {
    assertEquals(node("type:sample.Store").detail, "trait")
    assertEquals(node("type:sample.MemStore").detail, "class")
    assertEquals(node("type:sample.NullStore").detail, "object")
    assert(graph.node("type:sample.Service").exists(_.detail == "class"))
    assertEdge("type:sample.MemStore", "type:sample.Store", "implements")
    assertEdge("type:sample.NullStore", "type:sample.Store", "implements")
    assert(!hasEdge("type:sample.NullStore", "type:sample.Logging", "implements"))
    assertEdge("type:sample.Service", "type:sample.Store", "uses_type")
    assertEdge("method:(sample.Service).get", "type:sample.Item", "uses_type")
    assertEdge("method:(sample.Service).get", "type:sample.Service", "uses_type")
  }

  test("every edge joins nodes and the JSON is stable") {
    val ids = graph.allNodes.map(_.id).toSet
    assert(graph.allEdges.forall(e => ids(e.from) && ids(e.to)))
    assertEquals(graph.toJson, graph.toJson)
    assert(graph.toJson.startsWith("{\n  \"version\": 1,"))
  }

  test("arguments") {
    val args = Main.parse(List("--root", "r", "-o", "g.json", "--classpath", "a.jar:b.jar", "c1", "c2", "--classpath", "d.jar", "c3"))
    assertEquals(
      args.map(_.modules),
      Right(List(Module(List("a.jar", "b.jar"), List("c1", "c2")), Module(List("d.jar"), List("c3")))),
    )
    assertEquals(args.map(_.out), Right(Some(Paths.get("g.json"))))
    assert(Main.parse(List("--root", "r")).isLeft)
    assert(Main.parse(List("--bogus")).isLeft)
  }

  test("JSON strings are escaped") {
    assertEquals(Json.quote("a\"b\\c\nd\u0001"), "\"a\\\"b\\\\c\\nd\\u0001\"")
  }
}
