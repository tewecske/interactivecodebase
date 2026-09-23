package icb.scala

import java.nio.file.{Path, Paths}

class ExtractorSuite extends munit.FunSuite {

  // The test classes directory holds the TASTy of src/test/scala/sample.
  private lazy val graph: Graph = {
    val classes = Paths.get(classOf[ExtractorSuite].getProtectionDomain.getCodeSource.getLocation.toURI)
    val classpath = System.getProperty("java.class.path").split(java.io.File.pathSeparator).toList
    Main.run(
      Args(
        Paths.get("").toAbsolutePath,
        None,
        List(Module(classpath, List(classes.resolve("sample").toString))),
        guards = Map("Aspects.staff" -> "admin"),
      )
    )
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
    // Route handlers are numbered like closures ("routes$1").
    val ids = graph.allNodes.map(_.id).filterNot(_.matches(".*\\$\\d+"))
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

  private def route(key: String): Node = node(s"route:$key")

  test("routes: paths, parameters, endpoints and templates") {
    val routes = graph.allNodes.filter(_.kind == "route").map(_.name)
    assertEquals(
      routes.sorted,
      List(
        "DELETE /api/items/{?}",
        "GET /api/count",
        "GET /api/items/{id}",
        "GET /api/items/{page}",
        "PUT /api/items/{name}",
      ),
    )
    val page = route("GET /api/items/{page}")
    assertEquals(page.attrs("method"), "GET")
    assertEquals(page.attrs("pattern"), "/api/items/{page}")
    assertEquals(page.pos.map(p => (p.file, p.startLine)), Some(("src/test/scala/sample/web/Web.scala", 39)))
    assertEquals(route("DELETE /api/items/{?}").attrs.get("conditional"), Some("true"))
  }

  test("routes: handlers") {
    // A lambda is a node of its own, numbered in its member; a lambda that
    // only calls a def is that def.
    assertEquals(route("GET /api/items/{page}").attrs("handler"), "sample.web.Web.open$1")
    assertEquals(route("GET /api/count").attrs("handler"), "sample.web.Web.open$2")
    assertEquals(route("PUT /api/items/{name}").attrs("handler"), "sample.web.Web.update")
    assertEquals(route("GET /api/items/{id}").attrs("handler"), "sample.web.Web.closed$1")
    assertEdge("route:GET /api/items/{page}", "func:sample.web.Web.open$1", "handled_by")
    assertEdge("route:PUT /api/items/{name}", "func:sample.web.Web.update", "handled_by")
    val h = node("func:sample.web.Web.open$1")
    assertEquals(h.name, "Web.open$1")
    assertEquals(h.detail, "(page: Int, _$1: Request): Response")
    // The handler's calls are its own, not its member's.
    assertEdge("func:sample.web.Web.open$1", "func:sample.web.Web.render", "calls")
    assert(!hasEdge("func:sample.web.Web.open", "func:sample.web.Web.render", "calls"))
    assertEdge("func:sample.web.Web.open", "func:sample.web.Web.prefix", "calls")
  }

  test("routes: middleware and access") {
    val put = route("PUT /api/items/{name}")
    assertEquals(put.attrs("muxMiddleware"), "sample.web.Aspects.audit")
    assertEquals(put.attrs("middleware"), "sample.web.Aspects.requireUser")
    assertEquals(put.attrs("access"), "authenticated")
    assertEquals(put.attrs("accessEvidence"), "authenticated via sample.web.Aspects.requireUser")
    assertEdge("route:PUT /api/items/{name}", "func:sample.web.Aspects.requireUser", "guarded_by")
    assertEquals(route("GET /api/items/{page}").attrs("access"), "public")
    assert(!route("GET /api/items/{page}").attrs.contains("middleware"))
    // Configured in the test's guards.
    assertEquals(route("DELETE /api/items/{?}").attrs("access"), "admin")
  }

  private def sinks(kind: String): List[Node] = graph.allNodes.filter(_.kind == kind).toList

  /** The sink of kind called from caller, the only one there. */
  private def sinkOf(kind: String, caller: String): Node = {
    sinks(kind).filter(_.attrs("caller") == caller) match {
      case List(n) =>
        val from = if (caller.startsWith("(")) s"method:$caller" else s"func:$caller"
        assertEdge(from, n.id, "calls")
        n
      case other => fail(s"$kind sinks of $caller: $other; have ${sinks(kind).map(_.attrs("caller")).mkString(", ")}")
    }
  }

  test("sinks: Quill queries read from the quoted Scala") {
    val acc = "(sample.data.Accounts)"
    val byEmail = sinkOf("sink.sql", s"$acc.byEmail")
    assert(byEmail.id.startsWith("sink.sql:src/test/scala/sample/data/Data.scala:"), byEmail.id)
    assertEquals(byEmail.detail, "SELECT t0.email, t0.id, t0.name, t0.active FROM accounts t0")
    assertEquals(byEmail.attrs("dsl"), "quill")
    assertEquals(byEmail.attrs("resolved"), "true")
    assert(byEmail.attrs("callee").startsWith("io.getquill."), byEmail.attrs)
    assertEquals(sinkOf("sink.sql", s"$acc.names").detail, "SELECT t0.active, t0.name FROM accounts t0")
    assertEquals(
      sinkOf("sink.sql", s"$acc.withLogins").detail,
      "SELECT t0.id, t0.name, t0.email, t0.active, t1.account_id, t1.id, t1.at FROM accounts t0, logins t1",
    )
    assertEquals(sinkOf("sink.sql", s"$acc.create").detail, "INSERT INTO accounts (name, email, active) VALUES ($1, $2, $3) RETURNING id")
    assertEquals(sinkOf("sink.sql", s"$acc.rename").detail, "UPDATE accounts t0 SET name = $1 WHERE t0.id = $2")
    assertEquals(sinkOf("sink.sql", s"$acc.remove").detail, "DELETE FROM logins t0 WHERE t0.account_id = $1")
    assertEquals(sinkOf("sink.sql", s"$acc.audit").detail, "INSERT INTO audit_entry (id, message) VALUES ($1, $2)")
  }

  test("sinks: JDBC and Skunk SQL") {
    val count = sinkOf("sink.sql", "sample.data.Raw.count")
    assertEquals(count.detail, "SELECT count(*) FROM accounts WHERE active")
    assertEquals(count.attrs("callee"), "java.sql.Connection.prepareStatement")
    assertEquals(count.name, "Connection.prepareStatement")
    val skunk = sinkOf("sink.sql", "sample.data.Raw.skunkFind")
    assertEquals(skunk.detail, "SELECT id, email FROM accounts WHERE id = $1")
    assertEquals(skunk.attrs("dsl"), "skunk")
  }

  test("sinks: environment, files, HTTP and mail") {
    assertEquals(sinkOf("sink.env", "sample.data.Outside.home").detail, "HOME")
    assertEquals(sinkOf("sink.env", "sample.data.Outside.port").detail, "PORT")
    val read = sinkOf("sink.file", "sample.data.Outside.read")
    assertEquals(read.detail, "/etc/{?}")
    assertEquals(read.attrs("resolved"), "false")
    assertEquals(sinkOf("sink.http", "sample.data.Outside.fetch").detail, "https://example.com/api")
    assertEquals(sinkOf("sink.smtp", "sample.data.Outside.mail").attrs("callee"), "jakarta.mail.Transport.send")
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
    assertEquals(Main.parse(List("--guard", "a.B.c=admin", "c1")).map(_.guards), Right(Map("a.B.c" -> "admin")))
    assert(Main.parse(List("--guard", "a.B.c=root", "c1")).isLeft)
  }

  test("JSON strings are escaped") {
    assertEquals(Json.quote("a\"b\\c\nd\u0001"), "\"a\\\"b\\\\c\\nd\\u0001\"")
  }
}
