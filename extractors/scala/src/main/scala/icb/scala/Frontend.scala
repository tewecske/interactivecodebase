package icb.scala

import scala.collection.mutable
import scala.quoted.*

/** Finds the pages of a Laminar frontend routed by Waypoint, the requests
  * they make and the navigation between them, shaped like icb's Go pages
  * (internal/analysis/build.go addPages and addNavigation) but with page
  * nodes: the backend serves the frontend as a single-page app, so its
  * pages are not server routes.
  *
  *   - a page node per path of a Waypoint route (`Route.static(SignIn,
  *     root / "sign-in", basePath)`, `Route(encode, decode, pattern,
  *     basePath)`, `Route.withQuery` ...), "page:/app/notes/{noteId}", with
  *     the attributes method (GET), pattern, page (the page classes) and
  *     handler, like a route's; a path parameter is named after the page
  *     class's field of its type, and a base path that cannot be known is
  *     named after the value holding it ("{basePath}");
  *   - a handled_by edge to each view rendering the page: a def returning a
  *     Laminar element, called from a `case` matching the page class or from
  *     the renderer of a SplitRender `collect*` for it;
  *   - a requests edge from the page to an htmx_call node per request its
  *     views make, directly or through what they call: a use of a shared
  *     path template (gathedge's `ApiPath`) or zio-http `Endpoint`, whose
  *     method and path give the backend route exactly; the node is
  *     handled_by that route (resolved across modules by [[Main]]) and has
  *     the attributes method, url, trigger (fetch), via and target;
  *   - a navigates_to edge from the page to each page its views link to
  *     (`router.navigateTo`, `relativeUrlForPage`, trigger link), push
  *     (`pushState`, trigger navigate) or replace (`replaceState`, trigger
  *     redirect), found by the type of the page given; a def passing its
  *     parameter on, like a navigation link helper, is followed to its
  *     callers.
  */
private[scala] trait Frontend { self: Walk =>
  import q.reflect.*

  // A Waypoint route: the page class it stands for and its path. A route
  // whose matchEncode is empty only decodes URLs, so links never use it.
  private final case class PageRoute(cls: Symbol, path: String, at: Position, linkable: Boolean)
  // Views rendering the pages of some classes.
  private final case class Rendered(classes: List[Symbol], views: List[Symbol])
  private final case class Request(from: String, method: String, path: String, at: Position)
  private final case class Navigation(from: String, trigger: String, classes: List[Symbol], at: Position)
  // A call of a def of the inspected classes with its explicit arguments,
  // made by the node from, written in the member caller.
  private final case class OwnCall(from: String, caller: Symbol, callee: Symbol, args: List[Term], at: Position)

  private val pageRoutes = mutable.ListBuffer.empty[PageRoute]
  private val rendered = mutable.ListBuffer.empty[Rendered]
  private val requestSites = mutable.ListBuffer.empty[Request]
  private val navigations = mutable.ListBuffer.empty[Navigation]
  private val ownCalls = mutable.ListBuffer.empty[OwnCall]
  // The defs navigating to a page they are given: the def, the parameter's
  // index and the trigger.
  private val forwards = mutable.LinkedHashSet.empty[(Symbol, Int, String)]
  private val frontendCalls = mutable.HashSet.empty[(String, Int, Int)]
  private val frontendRefs = mutable.HashSet.empty[(String, Int, Int)]

  private val navTriggers = Map(
    "navigateTo" -> "link",
    "relativeUrlForPage" -> "link",
    "absoluteUrlForPage" -> "link",
    "pushState" -> "navigate",
    "replaceState" -> "redirect",
  )

  /** Records what t, a tree inside owner's right-hand side reached from the
    * node from, tells about pages.
    */
  private[scala] def addFrontend(from: String, t: Tree, owner: Symbol): Unit = t match {
    case a: Apply =>
      val (fn, args) = call(a)
      val sym = fn.symbol
      // The outermost application of a call comes first and has all its
      // argument lists.
      if (!sym.isNoSymbol && frontendCalls.add(posKey(fn.pos))) {
        val cls = sym.maybeOwner.fullName
        if (cls == "com.raquo.waypoint.Route$") waypointRoute(a, sym, args, owner)
        else if (cls.startsWith("com.raquo.waypoint.SplitRender") && sym.name.startsWith("collect")) splitRender(a, sym, args)
        else if (cls.startsWith("com.raquo.waypoint.Router") && navTriggers.contains(sym.name) && args.nonEmpty) {
          navigation(from, navTriggers(sym.name), args.head, owner, fn.pos)
        } else if (sym.isDefDef && own.contains(sym.maybeOwner) && isMemberDef(sym) && args.nonEmpty) {
          ownCalls += OwnCall(from, owner, sym, args, a.pos)
        }
      }
    case cd: CaseDef =>
      val classes = patternClasses(cd.pattern)
      if (classes.nonEmpty) {
        val views = viewsIn(cd.rhs)
        if (views.nonEmpty) rendered += Rendered(classes, views)
      }
    case r: Ref => request(from, r)
    case _      =>
  }

  // Waypoint routes.

  private def waypointRoute(a: Apply, sym: Symbol, args: List[Term], owner: Symbol): Unit = {
    val named = namedArgs(sym, args)
    val cls = pageClass(a).orElse(named.collectFirst { case ("staticPage", v) => classOf(v) }.flatten)
    for (c <- cls; pattern <- named.collectFirst { case ("pattern", v) => v }) {
      val base = named.collectFirst { case ("basePath", v) => basePathOf(v, owner) }.getOrElse("")
      val segs = nameParams(c, pathSegs(pattern, owner, 0))
      val path = (base.stripSuffix("/") + segs.map("/" + _).mkString) match {
        case "" => "/"
        case p  => p
      }
      val decodeOnly = named.exists {
        case ("matchEncode", v) => strip(v).symbol.name == "empty"
        case _                  => false
      }
      pageRoutes += PageRoute(c, path, a.pos, !decodeOnly)
    }
  }

  /** The page class of a route: the first type argument of its Route type. */
  private def pageClass(a: Apply): Option[Symbol] = {
    val tpe = a.tpe.widen.dealias
    val route = try Some(tpe.baseType(Symbol.requiredClass("com.raquo.waypoint.Route")))
    catch { case _: Exception => None }
    route.orElse(Some(tpe)).collect { case AppliedType(_, arg :: _) => arg }.flatMap(classOfType)
  }

  /** The arguments by parameter name. */
  private def namedArgs(sym: Symbol, args: List[Term]): List[(String, Term)] = {
    val params = explicitParams(sym).map(_.name)
    args.zipWithIndex.map {
      case (NamedArg(n, v), _) => n -> v
      case (v, i)              => params.lift(i).getOrElse("") -> v
    }
  }

  /** The term parameters a call gives explicitly, as [[call]] lists its
    * arguments.
    */
  private def explicitParams(sym: Symbol): List[Symbol] = {
    sym.paramSymss
      .filter(ps => ps.nonEmpty && ps.head.isTerm && !ps.head.flags.is(Flags.Given) && !ps.head.flags.is(Flags.Implicit))
      .flatten
  }

  private def basePathOf(t: Term, owner: Symbol): String = {
    val v = stringValue(t, owner, 0)
    if (!v.contains(Hole)) v
    else {
      strip(unnamed(t)) match {
        case r: Ref => s"/{${r.symbol.name}}"
        case _      => s"/$Hole"
      }
    }
  }

  // A path segment: a literal, or a parameter of a type.
  private sealed trait Seg
  private final case class Lit(s: String) extends Seg
  private final case class Param(tpe: String) extends Seg

  /** The segments of a Waypoint (url-dsl) pattern: `root / "notes" /
    * segment[Long]`, with `? params` for the query left out.
    */
  private def pathSegs(t: Term, owner: Symbol, depth: Int): List[Seg] = {
    if (depth > 20) return List(Lit(Hole))
    val u = unnamed(t)
    // segment[Long], with or without its implicit arguments.
    if (u.symbol.name == "segment") return List(Param(typeArgsOf(u).headOption.fold(Hole)(t => show(t))))
    strip(u) match {
      case Literal(StringConstant(s)) => s.split('/').toList.filter(_.nonEmpty).map(Lit(_))
      case a: Apply =>
        val (fn, args) = call(a)
        fn match {
          case Select(recv, "/") => pathSegs(recv, owner, depth + 1) ++ args.flatMap(pathSegs(_, owner, depth + 1))
          case Select(recv, "?") => pathSegs(recv, owner, depth + 1)
          // A literal segment converted to a path segment.
          case _ if args.sizeIs == 1 && isStringType(args.head) => pathSegs(args.head, owner, depth + 1)
          case _ => List(Lit(Hole))
        }
      case r: Ref =>
        val s = r.symbol
        if (s.isNoSymbol) List(Lit(Hole))
        else if (s.name == "root" && !members.contains(s)) Nil
        else {
          members.get(s).orElse(localsOf(owner).get(s)) match {
            case Some(rhs: Term) => pathSegs(rhs, owner, depth + 1)
            case _               => List(Lit(Hole))
          }
        }
      case _ => List(Lit(Hole))
    }
  }

  /** The segments as a path: each parameter named after the next field of
    * the page class of its type, else after its type.
    */
  private def nameParams(cls: Symbol, segs: List[Seg]): List[String] = {
    val fields = mutable.ListBuffer.from(
      explicitParams(cls.primaryConstructor).map(p => p.name -> show(info(p)))
    )
    segs.map {
      case Lit(s) => s
      case Param(tpe) =>
        fields.indexWhere(_._2 == tpe) match {
          case -1 => s"{${tpe.toLowerCase}}"
          case i  => s"{${fields.remove(i)._1}}"
        }
    }
  }

  // Rendering.

  /** The views a SplitRender collect* renders a page with:
    * collectStatic(page)(view), collectSignal[Page](render),
    * collectStaticPF { case ... }(render) ...
    */
  private def splitRender(a: Apply, sym: Symbol, args: List[Term]): Unit = {
    val classes = {
      if (sym.name.endsWith("PF")) args.headOption.toList.flatMap(caseClasses)
      else if (sym.name.startsWith("collectStatic") && args.sizeIs >= 2) classOf(args.head).toList
      else typeArgsOf(a).headOption.flatMap(classOfType).toList
    }
    // The renderer is the last argument; collectSignal[P] has no other.
    val views = if (args.sizeIs >= 2 || classes.nonEmpty) args.lastOption.toList.flatMap(viewsIn) else Nil
    if (classes.nonEmpty && views.nonEmpty) rendered += Rendered(classes, views)
  }

  /** The classes the case patterns inside t match. */
  private def caseClasses(t: Tree): List[Symbol] = {
    val out = mutable.ListBuffer.empty[Symbol]
    val traverser = new TreeTraverser {
      override def traverseTree(tree: Tree)(o: Symbol): Unit = tree match {
        case cd: CaseDef => out ++= patternClasses(cd.pattern)
        case _           => super.traverseTree(tree)(o)
      }
    }
    traverser.traverseTree(t)(Symbol.noSymbol)
    out.toList.distinct
  }

  /** The classes a pattern matches: `page: Page.Admin`, `Page.Words(q)`,
    * `Page.SignIn`, alternatives and tuples of them.
    */
  private def patternClasses(p: Tree): List[Symbol] = p match {
    case Bind(_, inner)          => patternClasses(inner)
    case Alternatives(ps)        => ps.flatMap(patternClasses)
    case TypedOrTest(inner, tpt) => classOfType(tpt.tpe).toList ++ patternClasses(inner)
    case Unapply(fun, _, pats) =>
      val extractor = strip(fun) match {
        case Select(qual, _) => classOf(qual).map(_.companionClass).filterNot(_.isNoSymbol).toList
        case _               => Nil
      }
      extractor ++ pats.flatMap(patternClasses)
    case Wildcard()            => Nil
    case Literal(_)            => Nil
    case r: Ref                => classOf(r).toList
    case _                     => Nil
  }

  /** The defs of the inspected classes returning a Laminar element that t
    * refers to.
    */
  private def viewsIn(t: Tree): List[Symbol] = {
    val out = mutable.LinkedHashSet.empty[Symbol]
    val traverser = new TreeTraverser {
      override def traverseTree(tree: Tree)(o: Symbol): Unit = {
        tree match {
          case r: Ref if isView(r.symbol) => out += r.symbol
          case _                          =>
        }
        super.traverseTree(tree)(o)
      }
    }
    traverser.traverseTree(t)(Symbol.noSymbol)
    out.toList
  }

  private def isView(s: Symbol): Boolean = {
    !s.isNoSymbol && s.isDefDef && own.contains(s.maybeOwner) && isMemberDef(s) && {
      def result(t: TypeRepr): TypeRepr = t match {
        case MethodType(_, _, res) => result(res)
        case PolyType(_, _, res)   => result(res)
        case other                 => other
      }
      try result(info(s)).dealias.baseClasses.exists(_.fullName == "com.raquo.laminar.nodes.ReactiveElement")
      catch { case _: Exception => false }
    }
  }

  // Requests and navigation.

  /** A use of a member holding a path template or an endpoint. */
  private def request(from: String, r: Ref): Unit = {
    val s = r.symbol
    if (!s.isNoSymbol && members.contains(s) && (s.isValDef || (s.isDefDef && s.paramSymss.isEmpty)) &&
      (isType(r, "zio.http.endpoint.Endpoint") || own.contains(r.tpe.widen.dealias.typeSymbol)) && frontendRefs.add(posKey(r.pos))) {
      apiRoute(s).foreach((m, path) => requestSites += Request(from, m, path, r.pos))
    }
  }

  private def navigation(from: String, trigger: String, arg: Term, owner: Symbol, at: Position): Unit = {
    paramIndex(owner, strip(unnamed(arg)).symbol) match {
      case Some(i) => forwards += ((owner, i, trigger))
      case None    => navigations += Navigation(from, trigger, classOf(arg).toList, at)
    }
  }

  private def paramIndex(owner: Symbol, s: Symbol): Option[Int] = {
    if (s.isNoSymbol || !s.flags.is(Flags.Param) || s.maybeOwner != owner) None
    else Some(explicitParams(owner).indexOf(s)).filter(_ >= 0)
  }

  /** Follows the defs navigating to a page they are given to their callers,
    * which navigate to the page they give.
    */
  private def forwardedNavigations(): List[Navigation] = {
    val calls = ownCalls.toList.groupBy(_.callee)
    var queue = forwards.toList
    while (queue.nonEmpty) {
      val next = for {
        (callee, i, trigger) <- queue
        c <- calls.getOrElse(callee, Nil)
        arg <- c.args.lift(i)
        j <- paramIndex(c.caller, strip(unnamed(arg)).symbol)
        if forwards.add((c.caller, j, trigger))
      } yield (c.caller, j, trigger)
      queue = next
    }
    for {
      (callee, i, trigger) <- forwards.toList
      c <- calls.getOrElse(callee, Nil)
      arg <- c.args.lift(i)
      if paramIndex(c.caller, strip(unnamed(arg)).symbol).isEmpty
    } yield Navigation(c.from, trigger, classOf(arg).toList, c.at)
  }

  // Output.

  /** Adds the page nodes and their edges; the code graph must be complete. */
  private[scala] def addPages(): Unit = if (pageRoutes.nonEmpty) {
    val routes = pageRoutes.toList.sortBy(r => posKey(r.at))
    val paths = routes.map(_.path).distinct
    val pageClasses = routes.map(_.cls).toSet
    def pageID(path: String) = s"page:$path"
    val targets = pageClasses.map { c =>
      val rs = routes.filter(_.cls == c)
      c -> rs.filter(_.linkable).map(_.path).distinct.ifEmpty(rs.map(_.path).distinct)
    }.toMap

    val next = mutable.HashMap.empty[String, mutable.ListBuffer[String]]
    for (e <- graph.allEdges if e.kind == "calls" || e.kind == "dispatches_to") {
      next.getOrElseUpdate(e.from, mutable.ListBuffer.empty) += e.to
    }
    def reach(starts: List[String]): Set[String] = {
      val seen = mutable.LinkedHashSet.from(starts)
      var queue = starts
      while (queue.nonEmpty && seen.size < 5000) {
        queue = queue.flatMap(id => next.getOrElse(id, Nil)).filter(seen.add)
      }
      seen.toSet
    }
    val requestsByFrom = requestSites.toList.groupBy(_.from)
    val navsByFrom = (navigations.toList ++ forwardedNavigations()).groupBy(_.from)

    for (path <- paths) {
      val id = pageID(path)
      val classes = routes.filter(_.path == path).map(_.cls).distinct
      val views = rendered.toList.filter(_.classes.exists(classes.contains)).flatMap(_.views).distinct
      val attrs = Map(
        "method" -> "GET",
        "pattern" -> path,
        "page" -> classes.map(qualified).mkString(","),
        "handler" -> views.headOption.fold(Hole)(v => unprefixed(funcID(v))),
        "access" -> "public",
      )
      val at = routes.find(_.path == path).flatMap(r => pos(r.at, withEnd = false))
      graph.addNode(Node(id, "page", path, pkgName(classes.head), classes.map(typeName).mkString(" "), at, attrs))
      for (v <- views) graph.addEdge(Edge(id, funcID(v), "handled_by"))

      val reached = reach(views.map(funcID))
      for (r <- reached.toList.flatMap(requestsByFrom.getOrElse(_, Nil)).sortBy(r => posKey(r.at)); p <- pos(r.at, withEnd = false)) {
        val key = s"${r.method} ${r.path}"
        val call = s"htmx_call:${p.file}:${p.startLine}:${p.startCol}"
        val callAttrs = Map("method" -> r.method, "url" -> r.path, "values" -> r.path, "trigger" -> "fetch", "via" -> unprefixed(r.from), "target" -> key)
        graph.addNode(Node(call, "htmx_call", key, "", "", Some(p), callAttrs))
        graph.addEdge(Edge(id, call, "requests", Some(p)))
        graph.addEdge(Edge(call, s"route:$key", "handled_by"))
      }
      val seen = mutable.HashSet.empty[(String, String)]
      for {
        n <- reached.toList.flatMap(navsByFrom.getOrElse(_, Nil)).sortBy(n => posKey(n.at))
        c <- n.classes if pageClasses(c)
        target <- targets(c)
        if seen.add((pageID(target), n.trigger))
      } {
        val attrs = Map("trigger" -> n.trigger, "via" -> unprefixed(n.from))
        graph.addEdge(Edge(id, pageID(target), "navigates_to", pos(n.at, withEnd = false), attrs))
      }
    }
  }

  // Trees and types.

  private def unnamed(t: Term): Term = t match {
    case NamedArg(_, v) => v
    case other          => other
  }

  private def classOf(t: Term): Option[Symbol] = {
    try classOfType(unnamed(t).tpe)
    catch { case _: Exception => None }
  }

  private def classOfType(t: TypeRepr): Option[Symbol] = {
    val s = t.widen.dealias.typeSymbol
    if (s.isClassDef) Some(s) else None
  }

  private def typeArgsOf(t: Term): List[TypeRepr] = t match {
    case Apply(fn, _)        => typeArgsOf(fn)
    case TypeApply(_, targs) => targs.map(_.tpe)
    case Inlined(_, Nil, e)  => typeArgsOf(e)
    case _                   => Nil
  }

  private def isStringType(t: Term): Boolean = {
    try t.tpe.widen.dealias.typeSymbol.fullName == "java.lang.String"
    catch { case _: Exception => false }
  }

  extension [A](xs: List[A]) private def ifEmpty(other: => List[A]): List[A] = if (xs.isEmpty) other else xs
}
