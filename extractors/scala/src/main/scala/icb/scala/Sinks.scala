package icb.scala

import scala.collection.mutable
import scala.quoted.*

/** Finds sinks, the calls that reach outside the program, shaped like icb's
  * Go sinks (internal/analysis/sinks.go and build.go addSinks):
  *
  *   - a node per call site, "sink.sql:path/File.scala:12:5", of kind
  *     sink.sql, sink.http, sink.file, sink.env or sink.smtp; its detail is
  *     what the call touches (SQL text, URL, file name, variable), "{?}"
  *     standing for what cannot be known, and its attributes are callee,
  *     caller and resolved;
  *   - a calls edge to it from the def, val or route handler it is written in.
  *
  * SQL comes from JDBC (prepareStatement, Statement.executeQuery ...), from
  * Skunk's sql interpolator, whose arguments become $1, $2 ..., and from
  * Quill's ctx.run, whose query is read from the quoted Scala (see
  * [[Quill]]): TASTy holds the code before Quill's macros expand it. icb
  * parses the SQL on import to link the tables it touches.
  *
  * The other sinks are zio-http's Client, sttp and java.net.http (sink.http);
  * java.nio.file.Files, scala.io.Source and java.io streams (sink.file);
  * sys.env, System.getenv, zio.System's env and ZIO.config (sink.env); and
  * Jakarta or javax Mail's Transport (sink.smtp).
  */
private[scala] trait Sinks { self: Walk =>
  import q.reflect.*

  // The call sites already made sinks, so that the inner Apply of a call
  // with several argument lists is not a second one.
  private val sinkSites = mutable.HashSet.empty[(String, Int, Int)]
  // Each member's local vals, by symbol.
  private val localVals = mutable.HashMap.empty[Symbol, Map[Symbol, Term]]

  private final case class Found(kind: String, callee: String, values: List[String], attrs: Map[String, String] = Map.empty)

  private val httpClientMethods = Set("batched", "request", "streaming", "stream", "get", "post", "put", "delete", "patch", "head", "socket")
  private val fileStreams = Set("java.io.FileInputStream", "java.io.FileOutputStream", "java.io.FileReader", "java.io.FileWriter", "java.io.RandomAccessFile")
  private val jdbcStatementMethods = Set("executeQuery", "executeUpdate", "executeLargeUpdate", "execute", "addBatch")

  /** Adds the sink t is, if any, called from the node from. t is a tree
    * inside owner's right-hand side.
    */
  private[scala] def addSink(from: String, t: Tree, owner: Symbol): Unit = {
    val found = t match {
      case a: Apply                => sinkCall(a, owner)
      case r: Ref if isSysEnv(r)   => Some(Found("sink.env", "scala.sys.env", List("{?}")))
      case _                       => None
    }
    for (f <- found; at <- pos(t.pos, withEnd = false) if sinkSites.add(posKey(t.pos))) {
      val id = s"${f.kind}:${at.file}:${at.startLine}:${at.startCol}"
      val resolved = f.values.nonEmpty && f.values.forall(v => !v.contains(Hole))
      val attrs = f.attrs ++ Map("callee" -> f.callee, "caller" -> unprefixed(from), "resolved" -> resolved.toString)
      val name = f.callee.split('.').takeRight(2).mkString(".")
      graph.addNode(Node(id, f.kind, name, pkgName(owner), f.values.mkString("\n"), Some(at), attrs))
      graph.addEdge(Edge(from, id, "calls", Some(at)))
    }
  }

  private[scala] val Hole = "{?}"

  private def sinkCall(t: Apply, owner: Symbol): Option[Found] = {
    val (fn, args) = call(t)
    val sym = fn.symbol
    if (sym.isNoSymbol) return None
    val cls = ownerName(sym)
    val name = sym.name
    val callee = s"$cls.$name"
    def str(i: Int): List[String] = List(args.lift(i).fold(Hole)(stringValue(_, owner, 0)))

    if (name == "<init>") {
      if (fileStreams(cls)) Some(Found("sink.file", cls, str(0))) else None
    } else if (cls.startsWith("io.getquill") && (name == "run" || name == "stream") && args.nonEmpty) {
      Some(quillSink(fn, args.head, callee, owner))
    } else if (cls.startsWith("skunk") && name == "fragmentFromParts" && args.nonEmpty) {
      Some(Found("sink.sql", callee, List(skunkSQL(args.head)), Map("dsl" -> "skunk")))
    } else if (cls == "java.sql.Connection" && (name == "prepareStatement" || name == "prepareCall") && args.nonEmpty) {
      Some(Found("sink.sql", callee, str(0)))
    } else if ((cls == "java.sql.Statement" || cls == "java.sql.PreparedStatement") && jdbcStatementMethods(name) && args.nonEmpty &&
      isString(args.head)) {
      Some(Found("sink.sql", callee, str(0)))
    } else if (cls == "zio.http.ZClient" && httpClientMethods(name)) {
      Some(Found("sink.http", callee, List(urlValue(args, owner))))
    } else if ((cls.startsWith("sttp.client") && name == "send") || (cls == "java.net.http.HttpClient" && name.startsWith("send"))) {
      Some(Found("sink.http", callee, List(urlValue(args, owner))))
    } else if (cls == "java.nio.file.Files") {
      Some(Found("sink.file", callee, str(0)))
    } else if (cls == "scala.io.Source" && (name == "fromFile" || name == "fromResource" || name == "fromURI")) {
      Some(Found("sink.file", callee, str(0)))
    } else if (cls == "java.lang.System" && name == "getenv") {
      Some(Found("sink.env", callee, if (args.isEmpty) List(Hole) else str(0)))
    } else if (cls == "zio.System" && name.startsWith("env")) {
      Some(Found("sink.env", callee, if (args.isEmpty) List(Hole) else str(0)))
    } else if (cls == "zio.ZIO" && name == "config") {
      // The configuration's type names what is read.
      val what = t.tpe.widen.dealias match {
        case AppliedType(_, targs) if targs.nonEmpty => show(targs.last)
        case _                                        => Hole
      }
      Some(Found("sink.env", callee, List(what)))
    } else if ((cls == "jakarta.mail.Transport" || cls == "javax.mail.Transport") && (name == "send" || name == "sendMessage")) {
      Some(Found("sink.smtp", callee, Nil))
    } else {
      fn match {
        // sys.env.get("NAME"), sys.env("NAME"), sys.env.getOrElse("NAME", ...)
        case Select(env: Ref, m) if isSysEnv(env) && args.nonEmpty && Set("get", "apply", "getOrElse", "contains")(m) =>
          sinkSites += posKey(env.pos)
          Some(Found("sink.env", "scala.sys.env", str(0)))
        case _ => None
      }
    }
  }

  /** The class or object a symbol is a member of, without "$": "zio.System". */
  private def ownerName(sym: Symbol): String = {
    val o = sym.maybeOwner
    if (o.isNoSymbol) "" else o.fullName.replace("$", "")
  }

  private def isSysEnv(r: Ref): Boolean = {
    val s = r.symbol
    !s.isNoSymbol && s.name == "env" && ownerName(s) == "scala.sys.package"
  }

  private def isString(t: Term): Boolean = {
    try t.tpe.widen.dealias.typeSymbol.fullName == "java.lang.String"
    catch { case _: Exception => false }
  }

  /** Skunk's fragment parts: "SELECT ... WHERE id = $1". */
  private def skunkSQL(parts: Term): String = {
    val elems = strip(parts) match {
      case a: Apply => call(a)._2
      case _        => Nil
    }
    if (elems.isEmpty) return Hole
    var n = 0
    elems.map { e =>
      strip(e) match {
        case a: Apply =>
          val (f, as) = call(a)
          f.symbol.name match {
            case "apply" if ownerName(f.symbol).endsWith("Str") => as.headOption.map(strip).collect { case Literal(StringConstant(s)) => s }.getOrElse(Hole)
            case "apply" if ownerName(f.symbol).endsWith("Par") => n += 1; "$" + n
            case _                                              => Hole
          }
        case _ => Hole
      }
    }.mkString
  }

  /** The URL of an HTTP call: the first string among its arguments that
    * looks like one.
    */
  private def urlValue(args: List[Term], owner: Symbol): String = {
    var found = Option.empty[String]
    val traverser = new TreeTraverser {
      override def traverseTree(t: Tree)(o: Symbol): Unit = if (found.isEmpty) {
        t match {
          case s: Term if isString(s) =>
            val v = stringValue(s, owner, 0)
            if (v.contains("://") || v.startsWith("/")) found = Some(v) else super.traverseTree(t)(o)
          case _ => super.traverseTree(t)(o)
        }
      }
    }
    args.foreach(traverser.traverseTree(_)(owner))
    found.getOrElse(Hole)
  }

  /** The value of a string expression: literals, vals, `+` and s"..."
    * interpolation, with "{?}" for what cannot be known. Paths and URLs
    * built from one string (Paths.get(s), URL.decode(s)) are that string.
    */
  private[scala] def stringValue(t: Term, owner: Symbol, depth: Int): String = {
    if (depth > 20) return Hole
    strip(t) match {
      case Literal(StringConstant(s)) => s
      case Inlined(_, _, e)           => stringValue(e, owner, depth + 1)
      case Block(_, e)                => stringValue(e, owner, depth + 1)
      case a: Apply =>
        val (fn, args) = call(a)
        val name = fn.symbol.name
        fn match {
          case Select(qual, "+") if args.sizeIs == 1 =>
            stringValue(qual, owner, depth + 1) + stringValue(args.head, owner, depth + 1)
          case Select(sc, "s" | "f" | "raw") if isType(sc, "scala.StringContext") =>
            val parts = strip(sc) match {
              case b: Apply => call(b)._2.map(p => stringValue(p, owner, depth + 1))
              case _        => Nil
            }
            if (parts.isEmpty) Hole
            else parts.head + parts.tail.zip(args).map((p, arg) => stringValue(arg, owner, depth + 1) + p).mkString
          case _ if args.nonEmpty && (name == "get" || name == "of" || name == "decode" || name == "unsafeParse" || name == "<init>" ||
                name == "apply" || name == "fromString") && isString(args.head) =>
            stringValue(args.head, owner, depth + 1)
          case _ if args.isEmpty && (name == "toOption" || name == "get") =>
            fn match {
              case Select(qual, _) => stringValue(qual, owner, depth + 1)
              case _               => Hole
            }
          case _ => Hole
        }
      case Select(qual, "toOption" | "get") => stringValue(qual, owner, depth + 1)
      case r: Ref =>
        val s = r.symbol
        if (s.isNoSymbol) Hole
        else if (s.isValDef && !s.flags.is(Flags.Mutable) && members.contains(s)) {
          members(s) match {
            case rhs: Term => stringValue(rhs, owner, depth + 1)
            case _         => Hole
          }
        } else localsOf(owner).get(s).fold(Hole)(stringValue(_, owner, depth + 1))
      case _ => Hole
    }
  }

  /** The local vals of owner's right-hand side. */
  private[scala] def localsOf(owner: Symbol): Map[Symbol, Term] = {
    localVals.getOrElseUpdate(
      owner, {
        val out = mutable.HashMap.empty[Symbol, Term]
        val traverser = new TreeTraverser {
          override def traverseTree(t: Tree)(o: Symbol): Unit = {
            t match {
              case v: ValDef if !v.symbol.flags.is(Flags.Mutable) => v.rhs.foreach(out(v.symbol) = _)
              case _                                              =>
            }
            super.traverseTree(t)(o)
          }
        }
        members.get(owner).foreach(traverser.traverseTree(_)(owner))
        out.toMap
      },
    )
  }

  private def quillSink(fn: Term, query: Term, callee: String, owner: Symbol): Found = {
    val ctx = fn match {
      case Select(qual, _) => Some(qual)
      case _               => None
    }
    val q = quillQuery(query, ctx, owner)
    val attrs = Map("dsl" -> "quill") ++ (if (q.partial) Map("partial" -> "true") else Map.empty)
    Found("sink.sql", callee, List(q.sql), attrs)
  }
}
