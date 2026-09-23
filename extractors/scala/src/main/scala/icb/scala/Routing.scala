package icb.scala

import scala.collection.mutable
import scala.quoted.*

/** Finds zio-http routes, shaped like icb's Go routes
  * (internal/analysis/build.go addRoutes):
  *
  *   - a route node per method and pattern, "route:GET /api/notes/{id}", with
  *     the attributes method, pattern, handler, middleware, muxMiddleware,
  *     access (public, authenticated, admin or guest), optionalAuth,
  *     accessEvidence and conditional;
  *   - a handled_by edge to its handler: a def or val of the inspected
  *     classes when the handler only calls one ("handler(create)"), else a
  *     node of its own for the handler expression, named after the member it
  *     is written in and numbered in source order ("func:app.NoteRoutes.routes$1");
  *   - a guarded_by edge to each aspect deciding its access.
  *
  * Routes are read from the Routes and Route vals and defs by a small
  * constant evaluator over the typed trees: string literals and vals,
  * `Method.GET / "api" / long("id")` paths (parameters become "{id}", what
  * cannot be known "{?}"), `->`, `Routes(...)`, `++`, `@@` aspects,
  * zio-http `Endpoint`s with `implement*`, and path templates such as
  * gathedge's `ApiPath1[Long](GET, "/api/words/{id}")`.
  *
  * The routes served are those of the Routes values no other one includes.
  * Aspects applied there wrap the whole server (muxMiddleware); those
  * applied inside, in the Routes values it includes, are the route's own
  * middleware. Both are listed outermost first. Access comes from the
  * aspects' names (authenticat*, requireAuth, admin*, optional*User) or from
  * icb.yaml's auth.guards.
  */
private[scala] trait Routing { self: Walk =>
  import q.reflect.*

  // Values.

  private sealed trait V
  private final case class Str(s: String) extends V
  private final case class Meth(m: String) extends V
  // A PathCodec: its segments, "{name}" for a parameter.
  private final case class PathV(segs: List[String]) extends V
  private final case class Pattern(method: String, segs: List[String]) extends V
  // A method and path template kept in plain values, like gathedge's ApiPath.
  private final case class Template(method: String, path: String) extends V
  private final case class EndpointV(p: Pattern) extends V
  private final case class HandlerV(h: HandlerRef) extends V
  private final case class RoutesV(routes: List[RouteV]) extends V
  private case object Unknown extends V

  private sealed trait HandlerRef
  // A def or val of the inspected classes.
  private final case class Member(sym: Symbol) extends HandlerRef
  // An expression, usually a lambda, written in the member owner.
  private final case class Expr(tree: Term, owner: Symbol) extends HandlerRef

  // An aspect or a function wrapping routes; depth counts the Routes values
  // between the root and where it is applied.
  private final case class Aspect(name: String, sym: Option[Symbol], depth: Int)

  private final case class RouteV(p: Pattern, handler: Option[HandlerRef], aspects: List[Aspect], at: Position)

  private val httpMethods = Set("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT", "ANY")
  private val paramCodecs = Set("string", "int", "long", "uuid", "boolean")

  private val adminRE = "(?i)admin".r.unanchored
  private val authRE = "(?i)authenticat|require.?(auth|user|session|login)|logged.?in|signed.?in".r.unanchored
  private val optionalRE = "(?i)optional.?(user|auth|session)|maybe.?(user|auth|session)".r.unanchored
  private val accessRank = Map("public" -> 0, "authenticated" -> 1, "guest" -> 2, "admin" -> 3)

  // The evaluated members and the Routes members another one includes.
  private val memo = mutable.HashMap.empty[Symbol, V]
  private val evaluating = mutable.HashSet.empty[Symbol]
  private val included = mutable.HashSet.empty[Symbol]
  private val locals = mutable.HashMap.empty[Symbol, Term]
  private var routes = List.empty[RouteV]
  // The handler expressions by position, with their node IDs.
  private val handlerIDs = mutable.LinkedHashMap.empty[(String, Int, Int), (String, Expr)]

  /** Evaluates the Routes and Route members and keeps the routes of those
    * no other one includes.
    */
  private[scala] def findRoutes(): Unit = {
    val candidates = members.keys.filter(s => own.contains(s.owner) && isRoutesType(resultType(s))).toList.sortBy(symKey)
    candidates.foreach(evalMember)
    val roots = candidates.filterNot(included)
    val seen = mutable.HashSet.empty[String]
    routes = for {
      root <- roots
      rt <- memo.get(root).collect { case RoutesV(rs) => rs }.getOrElse(Nil).sortBy(r => posKey(r.at))
      if seen.add(key(rt.p))
    } yield rt
    // Number each member's handler expressions in source order.
    val exprs = routes.flatMap(_.handler).collect { case e: Expr => e }.distinctBy(e => posKey(e.tree.pos))
    for ((owner, es) <- exprs.groupBy(_.owner); (e, i) <- es.sortBy(e => posKey(e.tree.pos)).zipWithIndex) {
      handlerIDs(posKey(e.tree.pos)) = (s"${funcID(owner)}$$${i + 1}", e)
    }
  }

  /** Reports whether t is a handler expression with a node of its own. */
  private[scala] def isHandler(t: Tree): Boolean = handlerIDs.nonEmpty && handlerIDs.contains(posKey(t.pos))

  /** Adds the route and handler nodes; the members must have been added. */
  private[scala] def addRoutes(): Unit = {
    for ((id, e @ Expr(tree, owner)) <- handlerIDs.values) {
      val (body, detail) = lambda(tree) match {
        case Some(d) => (d.rhs.getOrElse(tree), signature(d.symbol))
        case None    => (tree, "")
      }
      val name = funcName(owner) + id.substring(id.lastIndexOf('$'))
      graph.addNode(Node(id, funcKind(owner), name, pkgName(owner), detail, pos(tree.pos, withEnd = true)))
      addCalls(id, body, owner)
    }
    for (rt <- routes) addRoute(rt)
  }

  private def addRoute(rt: RouteV): Unit = {
    val k = key(rt.p)
    val id = s"route:$k"
    val handlerID = rt.handler.map {
      case Member(sym) => funcID(sym)
      case e: Expr     => handlerIDs(posKey(e.tree.pos))._1
    }
    val (mux, inner) = rt.aspects.partition(_.depth == 0)
    val attrs = mutable.LinkedHashMap("method" -> rt.p.method, "pattern" -> path(rt.p), "handler" -> handlerID.fold("{?}")(unprefixed))
    if (inner.nonEmpty) attrs("middleware") = inner.map(_.name).mkString(",")
    if (mux.nonEmpty) attrs("muxMiddleware") = mux.map(_.name).mkString(",")
    if (k.contains("{?}")) attrs("conditional") = "true"

    // The strongest role any aspect enforces; optional ones only consult
    // the session.
    val roles = rt.aspects.map(a => a -> role(a))
    val access = roles.flatMap(_._2).maxByOption(accessRank).getOrElse("public")
    attrs("access") = access
    val optional = if (access == "public") rt.aspects.filter(a => optionalRE.matches(simpleName(a))) else Nil
    if (optional.nonEmpty) attrs("optionalAuth") = "true"
    val evidence = roles.collect { case (a, Some(r)) => s"$r via ${a.name}" } ++ optional.map(a => s"consults ${a.name}")
    if (evidence.nonEmpty) attrs("accessEvidence") = evidence.mkString("; ")

    graph.addNode(Node(id, "route", k, detail = "", pos = pos(rt.at, withEnd = false), attrs = attrs.toMap))
    handlerID.foreach(h => graph.addEdge(Edge(id, h, "handled_by")))
    for ((a, r) <- roles; sym <- a.sym) {
      r.foreach(r => graph.addEdge(Edge(id, funcID(sym), "guarded_by", attrs = Map("role" -> r))))
    }
    for (a <- optional; sym <- a.sym) graph.addEdge(Edge(id, funcID(sym), "guarded_by", attrs = Map("optional" -> "true")))
  }

  /** The access an aspect enforces: configured, or from its name. */
  private def role(a: Aspect): Option[String] = {
    guards.get(a.name).orElse(guards.collectFirst { case (f, r) if a.name.endsWith("." + f) => r }).orElse {
      val n = simpleName(a)
      if (adminRE.matches(n)) Some("admin")
      else if (authRE.matches(n)) Some("authenticated")
      else None
    }
  }

  private def simpleName(a: Aspect): String = a.name.substring(a.name.lastIndexOf('.') + 1)

  private def key(p: Pattern): String = s"${p.method} ${path(p)}"

  private def path(p: Pattern): String = p.segs.mkString("/", "/", "")

  private[scala] def unprefixed(id: String): String = id.substring(id.indexOf(':') + 1)

  // The evaluator.

  private def evalMember(sym: Symbol): V = {
    memo.get(sym) match {
      case Some(v)                    => v
      case None if evaluating(sym)    => Unknown
      case None =>
        evaluating += sym
        val v = members.get(sym) match {
          case Some(rhs: Term) => eval(rhs, sym)
          case _               => Unknown
        }
        evaluating -= sym
        memo(sym) = v
        v
    }
  }

  /** The value of a member referred to from another one: its routes' aspects
    * move one level further from the root.
    */
  private def refMember(sym: Symbol): V = {
    evalMember(sym) match {
      case RoutesV(rs) =>
        included += sym
        RoutesV(rs.map(r => r.copy(aspects = r.aspects.map(a => a.copy(depth = a.depth + 1)))))
      case v => v
    }
  }

  private def eval(tree: Term, owner: Symbol): V = {
    strip(tree) match {
      case Literal(StringConstant(s)) => Str(s)
      case Block(stats, expr) =>
        stats.foreach {
          case v: ValDef => v.rhs.foreach(locals(v.symbol) = _)
          case _         =>
        }
        if (lambda(expr).isDefined) Unknown else eval(expr, owner)
      case Inlined(_, bindings, e) =>
        bindings.foreach {
          case v: ValDef => v.rhs.foreach(locals(v.symbol) = _)
          case _         =>
        }
        eval(e, owner)
      case t: Apply => evalApply(t, owner)
      case t @ (_: Ident | _: Select) => evalRef(t, owner)
      case _ => Unknown
    }
  }

  /** The routes of `routes @@ aspect` for an aspect with a context, which
    * zio-http inlines as `proxy.self.transform(route => ...aspect...)`, the
    * proxy bound to `routes.@@`.
    */
  private def contextAspect(receiver: Option[Term], args: List[Term], owner: Symbol): Option[V] = {
    val routes = receiver.map(strip).collect {
      case Select(proxy, self) if self.endsWith("$inline$self") && locals.contains(strip(proxy).symbol) =>
        strip(locals(strip(proxy).symbol))
    }
    routes.collect { case Select(r, "@@") => eval(r, owner) }.collect { case RoutesV(rs) =>
      var found = Option.empty[Term]
      val traverser = new TreeTraverser {
        override def traverseTree(t: Tree)(o: Symbol): Unit = if (found.isEmpty) {
          t match {
            case r: Ref if isAspectType(r) => found = Some(r)
            case _                         => super.traverseTree(t)(o)
          }
        }
      }
      args.foreach(traverser.traverseTree(_)(owner))
      val a = found.fold(Aspect("{?}", None, 0))(aspect)
      RoutesV(rs.map(r => r.copy(aspects = a :: r.aspects)))
    }
  }

  private def isAspectType(t: Term): Boolean = isType(t, "zio.http.HandlerAspect") || isType(t, "zio.http.Middleware")

  private def evalRef(t: Term, owner: Symbol): V = {
    val sym = t.symbol
    if (sym.isNoSymbol) Unknown
    else if (httpMethods(sym.name) && !sym.isDefDef) {
      if (isType(t, "zio.http.RoutePattern")) Pattern(sym.name, Nil) else Meth(sym.name)
    } else if (members.contains(sym) && (sym.isValDef || sym.paramSymss.isEmpty)) refMember(sym)
    else if (locals.contains(sym)) eval(locals(sym), owner)
    else if (sym.name == "Root" || (sym.name == "empty" && isType(t, "zio.http.codec.PathCodec"))) PathV(Nil)
    else if (sym.name == "trailing") PathV(List("{trailing...}"))
    else t match {
      // A builder step of an endpoint or routes, e.g. endpoint.outErrors[E]
      // before its argument list.
      case Select(qual, _) if sym.isDefDef =>
        eval(qual, owner) match {
          case v @ (_: EndpointV | _: RoutesV) => v
          case _                               => Unknown
        }
      case _ => Unknown
    }
  }

  private def evalApply(t: Apply, owner: Symbol): V = {
    val (fn, args) = call(t)
    val sym = fn.symbol
    val name = if (sym.isNoSymbol) "" else sym.name
    val receiver = fn match {
      case Select(qual, _) => Some(qual)
      case _               => None
    }
    def recv: V = receiver.fold(Unknown: V)(eval(_, owner))
    def arg(i: Int): V = if (args.sizeIs > i) eval(args(i), owner) else Unknown
    lazy val values = args.map(eval(_, owner))

    contextAspect(receiver, args, owner).getOrElse(name match {
      case "/" if receiver.isDefined && args.sizeIs == 1 => slash(recv, arg(0), t)
      case "+" if receiver.isDefined =>
        (recv, arg(0)) match {
          case (Str(a), Str(b)) => Str(a + b)
          case _                => Unknown
        }
      case "->" if receiver.isDefined && args.sizeIs == 1 =>
        pattern(recv) match {
          case Some(p) => RoutesV(List(RouteV(p, handlerOf(args(0), owner), Nil, t.pos)))
          case None    => Unknown
        }
      case "@@" if receiver.isDefined && args.sizeIs == 1 =>
        recv match {
          case RoutesV(rs) =>
            val a = aspect(args(0))
            RoutesV(rs.map(r => r.copy(aspects = a :: r.aspects)))
          case v => v
        }
      case "++" if receiver.isDefined && args.sizeIs == 1 =>
        (recv, arg(0)) match {
          case (RoutesV(a), RoutesV(b)) => RoutesV(a ++ b)
          case (RoutesV(a), _)          => RoutesV(a)
          case (_, RoutesV(b))          => RoutesV(b)
          case _                        => Unknown
        }
      case n if n.startsWith("implement") && receiver.isDefined && args.nonEmpty =>
        recv match {
          case EndpointV(p) => RoutesV(List(RouteV(p, handlerOf(args(0), owner), Nil, t.pos)))
          case _            => Unknown
        }
      case "fromString" if isType(t, "zio.http.Method") =>
        arg(0) match {
          case Str(m) if httpMethods(m) => Meth(m)
          case _                        => Unknown
        }
      case _ => evalCall(t, sym, name, args, values, recv, owner)
    })
  }

  private def evalCall(t: Apply, sym: Symbol, name: String, args: List[Term], values: => List[V], recv: => V, owner: Symbol): V = {
    if (isType(t, "zio.http.codec.PathCodec") || isType(t, "zio.http.codec.SegmentCodec")) {
      (name, values) match {
        case (n, List(Str(p))) if paramCodecs(n)          => PathV(List(s"{$p}"))
        case ("path" | "literal" | "apply", List(Str(s))) => PathV(split(s))
        case ("segment", List(v: PathV))                  => v
        case _                                            => Unknown
      }
    } else if (isType(t, "zio.http.RoutePattern")) {
      values.collectFirst {
        case Template(m, p) => Pattern(m, split(p))
      }.orElse((values match {
        case List(Meth(m), Str(p)) => Some(Pattern(m, split(p)))
        case _                     => None
      })).orElse(values.collectFirst { case p: Pattern => p }).getOrElse(Unknown)
    } else if (isType(t, "zio.http.endpoint.Endpoint")) {
      // Endpoint(pattern), then .out, .in, .outError and extensions of
      // the app's own, which keep the method and path.
      val fromArgs = values.collectFirst {
        case p: Pattern   => EndpointV(p)
        case e: EndpointV => e
      }
      fromArgs.orElse(recv match {
        case e: EndpointV => Some(e)
        case _            => None
      }).orElse(ownBody(sym, owner)).getOrElse(Unknown)
    } else if (isType(t, "zio.http.Handler")) {
      if (args.sizeIs == 1 && name != "@@") HandlerV(handlerRef(args(0), owner)) else HandlerV(Expr(t, owner))
    } else if (isType(t, "zio.http.Routes") || isType(t, "zio.http.Route")) {
      val routes = values.collect { case RoutesV(rs) => rs }
      if (sym.fullName == "zio.http.Routes$.apply" || sym.fullName == "zio.http.Routes.apply") RoutesV(routes.flatten)
      else if (routes.nonEmpty) {
        // A function wrapping routes: the app's own is middleware, a
        // library's (handleError, sandbox) is not.
        val wrapped = routes.flatten
        if (own.contains(sym.owner)) RoutesV(wrapped.map(r => r.copy(aspects = aspectOf(sym) :: r.aspects)))
        else RoutesV(wrapped)
      } else {
        recv match {
          case r: RoutesV => r
          case _          => ownBody(sym, owner).getOrElse(Unknown)
        }
      }
    } else {
      // A method and path template: ApiPath1[Long](GET, "/api/words/{id}").
      val ms = values.collect { case Meth(m) => m }
      val ps = values.collect { case Str(s) if s.startsWith("/") => s }
      if (ms.sizeIs == 1 && ps.sizeIs == 1) Template(ms.head, ps.head) else Unknown
    }
  }

  /** The value of a call of the app's own def returning routes or an
    * endpoint, its parameters unknown.
    */
  private def ownBody(sym: Symbol, owner: Symbol): Option[V] = {
    if (members.contains(sym) && sym.isDefDef) Some(refMember(sym)) else None
  }

  private def slash(left: V, right: V, t: Term): V = {
    val segs = right match {
      case Str(s)   => split(s)
      case PathV(s) => s
      case _        => List("{?}")
    }
    left match {
      case Meth(m)        => Pattern(m, segs)
      case Pattern(m, s)  => Pattern(m, s ++ segs)
      case PathV(s)       => PathV(s ++ segs)
      case Str(s)         => PathV(split(s) ++ segs)
      case _ if isType(t, "zio.http.RoutePattern") => Pattern("ANY", "{?}" :: segs)
      case _              => PathV("{?}" :: segs)
    }
  }

  private def pattern(v: V): Option[Pattern] = v match {
    case p: Pattern => Some(p)
    case Meth(m)    => Some(Pattern(m, Nil))
    case _          => None
  }

  private def split(s: String): List[String] = s.split('/').toList.filter(_.nonEmpty)

  /** What serves a route: the handler an argument evaluates to, else the
    * argument itself (implement's function).
    */
  private def handlerOf(arg: Term, owner: Symbol): Option[HandlerRef] = {
    ownRef(arg).map(Member(_)).orElse(eval(arg, owner) match {
      case HandlerV(h) => Some(h)
      case _           => Some(handlerRef(arg, owner))
    })
  }

  /** A member for a reference or a lambda that only calls it with its own
    * parameters (eta-expansion), else the expression.
    */
  private def handlerRef(arg: Term, owner: Symbol): HandlerRef = {
    val t = strip(arg)
    ownRef(t).map(Member(_)).getOrElse {
      val forwarded = lambda(t).flatMap { d =>
        val params = d.termParamss.flatMap(_.params).map(_.symbol)
        d.rhs.map(strip).collect { case a: Apply => call(a) }.collect {
          case (fn, as) if isOwnFunc(fn.symbol) && as.map(a => strip(a).symbol) == params => fn.symbol
        }
      }
      forwarded.fold(Expr(t, owner): HandlerRef)(Member(_))
    }
  }

  private def ownRef(t: Term): Option[Symbol] = strip(t) match {
    case r @ (_: Ident | _: Select) if isOwnFunc(r.symbol) => Some(r.symbol)
    case _                                                 => None
  }

  private def isOwnFunc(sym: Symbol): Boolean = {
    !sym.isNoSymbol && own.contains(sym.maybeOwner) &&
    ((sym.isDefDef && isMemberDef(sym)) || (sym.isValDef && isMemberVal(sym)))
  }

  private def aspect(t: Term): Aspect = strip(t) match {
    case a: Apply                         => aspectOf(call(a)._1.symbol)
    case r if locals.contains(r.symbol)   => aspect(locals(r.symbol))
    case r                                => aspectOf(r.symbol)
  }

  private def aspectOf(sym: Symbol): Aspect = {
    if (sym.isNoSymbol) Aspect("{?}", None, 0)
    else if (isOwnFunc(sym)) Aspect(unprefixed(funcID(sym)), Some(sym), 0)
    else Aspect(s"${sym.owner.fullName.replace("$", "")}.${sym.name}", None, 0)
  }

  // Trees.

  /** The function called and its explicit arguments, varargs spread;
    * implicit and using argument lists are left out.
    */
  private[scala] def call(t: Apply): (Term, List[Term]) = {
    def loop(t: Term): (Term, List[Term]) = t match {
      case Apply(fn, args) =>
        val (f, before) = loop(fn)
        val implicitList = fn.tpe.widen match {
          case mt: MethodType => mt.isImplicit
          case _              => false
        }
        (f, if (implicitList) before else before ++ args.flatMap(spread))
      case TypeApply(fn, _) => loop(fn)
      case Inlined(_, Nil, e) => loop(e)
      case other            => (other, Nil)
    }
    loop(t)
  }

  private[scala] def spread(t: Term): List[Term] = {
    def unInline(t: Term): Term = t match {
      case Inlined(_, Nil, e) => unInline(e)
      case other              => other
    }
    unInline(t) match {
      case Typed(e, _) =>
        unInline(e) match {
          case Repeated(elems, _) => elems
          case _                  => List(t)
        }
      case Repeated(elems, _) => elems
      case _                  => List(t)
    }
  }

  /** t without the wrappers that do not change its value. */
  private[scala] def strip(t: Term): Term = t match {
    case Inlined(_, Nil, e) => strip(e)
    case Typed(e, _)        => strip(e)
    case Block(Nil, e)      => strip(e)
    case TypeApply(e, _)    => strip(e)
    case other              => other
  }

  private[scala] def lambda(t: Term): Option[DefDef] = strip(t) match {
    case Block(List(d: DefDef), _: Closure) => Some(d)
    case _                                  => None
  }

  private[scala] def isType(t: Term, fullName: String): Boolean = {
    try {
      val s = t.tpe.widen.dealias.typeSymbol
      s.fullName == fullName || s.typeRef.baseClasses.exists(_.fullName == fullName)
    } catch { case _: Exception => false }
  }

  private def resultType(sym: Symbol): String = {
    def loop(t: TypeRepr): TypeRepr = t match {
      case MethodType(_, _, res) => loop(res)
      case PolyType(_, _, res)   => loop(res)
      case ByNameType(u)         => u
      case other                 => other
    }
    try loop(info(sym)).dealias.typeSymbol.fullName
    catch { case _: Exception => "" }
  }

  private def isRoutesType(name: String): Boolean = name == "zio.http.Routes" || name == "zio.http.Route"

  private def symKey(sym: Symbol): (String, Int, Int) = sym.pos.fold(("", 0, 0))(posKey)

  private[scala] def posKey(p: Position): (String, Int, Int) = {
    val file = try p.sourceFile.path catch { case _: Exception => "" }
    (file, p.start, p.end)
  }
}
