package icb.scala

import java.nio.file.{Path, Paths}
import scala.collection.mutable
import scala.quoted.*
import scala.tasty.inspector.*

/** Adds the code graph of the inspected TASTy to graph, shaped like icb's Go
  * analysis (internal/analysis/build.go):
  *
  *   - a func node per def or val of an object or a package ("func:pkg.Obj.name",
  *     "func:pkg.name"), a method node per def or val of a class or trait
  *     ("method:(pkg.Class).name"); overloads add their parameter types;
  *   - a type node per class ("class"), trait ("trait"), enum ("enum") and
  *     standalone object ("object"): "type:pkg.Name";
  *   - calls edges from a def to the defs and vals it refers to, lambdas and
  *     local defs included;
  *   - a call of an abstract method goes to an interface_call node
  *     ("interface_call:(pkg.Trait).name"), which dispatches_to the
  *     implementation in every concrete class that inherits it
  *     (class-hierarchy analysis);
  *   - implements edges from a class or object to the traits it extends;
  *   - uses_type edges from a def to its receiver, parameter and result types
  *     and from a class to its fields' types;
  *   - calls made in the operands of ZIO's parallel combinators (`<&>`,
  *     `zipPar`, `collectAllPar`, `foreachPar`, ...) carry the attributes
  *     parallel (the combinator's position, shared by the calls that run
  *     alongside each other) and branch (the operand's number from 1, or
  *     "each" for a function run once per element).
  *
  * Only the inspected classes become nodes: calls into libraries are left
  * out, except sinks (see [[Sinks]]). Routes adds the zio-http routes (see
  * [[Routing]]), Frontend a Laminar frontend's pages (see [[Frontend]]).
  */
final class Extractor(root: Path, graph: Graph, guards: Map[String, String] = Map.empty) extends Inspector {
  def inspect(using q: Quotes)(tastys: List[Tasty[q.type]]): Unit = {
    val walk = new Walk(root, graph, guards)
    walk.run(tastys.map(_.ast.asInstanceOf[walk.q.reflect.Tree]))
  }
}

private final class Walk(root: Path, val graph: Graph, val guards: Map[String, String])(using val q: Quotes)
    extends Routing
    with Sinks
    with Quill
    with Frontend {
  import q.reflect.*

  // Named classes, traits and objects of the inspected TASTy.
  private[scala] val own = mutable.LinkedHashSet.empty[Symbol]
  private[scala] val classDefs = mutable.ListBuffer.empty[ClassDef]
  // Abstract methods called, by interface_call node ID.
  private val abstractCalls = mutable.LinkedHashMap.empty[String, Symbol]

  // The right-hand side of every def and val of the inspected classes.
  private[scala] val members = mutable.HashMap.empty[Symbol, Tree]

  // The parallel and branch attributes of the calls being added (see
  // [[parallelBranches]]).
  private[scala] var parallelAttrs = Map.empty[String, String]

  def run(trees: List[Tree]): Unit = {
    trees.foreach(collect)
    // Routes first: their handlers become nodes of their own, which the
    // members they are written in leave out.
    findRoutes()
    classDefs.foreach(addClass)
    addRoutes()
    addDispatch()
    addPages()
  }

  private def collect(tree: Tree): Unit = tree match {
    case PackageClause(_, stats) => stats.foreach(collect)
    case cd: ClassDef =>
      own += cd.symbol
      classDefs += cd
      cd.body.foreach {
        case nested: ClassDef => collect(nested)
        case d: DefDef        => d.rhs.foreach(members(d.symbol) = _)
        case v: ValDef        => v.rhs.foreach(members(v.symbol) = _)
        case _                =>
      }
    case _ =>
  }

  private def addClass(cd: ClassDef): Unit = {
    val cls = cd.symbol
    if (hasTypeNode(cls)) {
      addType(cls)
      if (!cls.flags.is(Flags.Trait)) {
        for (base <- cls.typeRef.baseClasses if base != cls && base.flags.is(Flags.Trait) && hasTypeNode(base)) {
          addType(base)
          graph.addEdge(Edge(typeID(cls), typeID(base), "implements"))
        }
      }
      if (!cls.flags.is(Flags.Module)) {
        val fields = cd.constructor.termParamss.flatMap(_.params).map(_.tpt.tpe) ++
          cd.body.collect { case v: ValDef if isMemberVal(v.symbol) => v.tpt.tpe }
        for (t <- fields.flatMap(ownTypes).distinct) {
          graph.addEdge(Edge(typeID(cls), typeID(t), "uses_type"))
        }
      }
    }
    cd.body.foreach {
      case d: DefDef if isMemberDef(d.symbol) =>
        addFunc(d.symbol, d.pos)
        d.rhs.foreach(addCalls(funcID(d.symbol), _, d.symbol))
      case v: ValDef if isMemberVal(v.symbol) =>
        addFunc(v.symbol, v.pos)
        v.rhs.foreach(addCalls(funcID(v.symbol), _, v.symbol))
      case _ =>
    }
  }

  private[scala] def isMemberDef(sym: Symbol): Boolean = {
    !sym.isClassConstructor && !sym.flags.is(Flags.Deferred) && !skipped(sym)
  }

  private[scala] def isMemberVal(sym: Symbol): Boolean = {
    !sym.flags.is(Flags.ParamAccessor) && !sym.flags.is(Flags.Module) && !sym.flags.is(Flags.Deferred) && !skipped(sym)
  }

  // Compiler-generated members: case class methods, derived givens
  // ("derived$JsonCodec"), export forwarders.
  private[scala] def skipped(sym: Symbol): Boolean = {
    sym.flags.is(Flags.Synthetic) || sym.flags.is(Flags.Artifact) || sym.flags.is(Flags.Exported) || sym.name.contains("$")
  }

  private[scala] def addFunc(sym: Symbol, p: Position): Unit = {
    val id = funcID(sym)
    graph.addNode(Node(id, funcKind(sym), funcName(sym), pkgName(sym), signature(sym), pos(p, withEnd = true)))
    val receiver = sym.owner
    val receivers = if (receiver.flags.is(Flags.Module) || !hasTypeNode(receiver)) Nil else List(receiver)
    for (t <- (receivers ++ ownTypes(info(sym))).distinct) {
      addType(t)
      graph.addEdge(Edge(id, typeID(t), "uses_type"))
    }
  }

  private def addType(cls: Symbol): Unit = {
    val detail = {
      if (cls.flags.is(Flags.Trait)) "trait"
      else if (cls.flags.is(Flags.Module)) "object"
      else if (cls.flags.is(Flags.Enum)) "enum"
      else "class"
    }
    graph.addNode(Node(typeID(cls), "type", typeName(cls), pkgName(cls), detail, cls.pos.flatMap(pos(_, withEnd = false))))
  }

  /** Adds calls edges from the node from to what tree refers to and to the
    * sinks it calls (see [[Sinks]]), leaving out route handlers inside it,
    * which are nodes of their own.
    */
  private[scala] def addCalls(from: String, tree: Tree, owner: Symbol): Unit = {
    val deferred = deferredBranches(tree)
    val traverser = new TreeTraverser {
      override def traverseTree(t: Tree)(o: Symbol): Unit = {
        if ((t eq tree) || !isHandler(t)) {
          val branchVal = t match {
            case v: ValDef if deferred.contains(v.name) => v.rhs
            case _                                      => None
          }
          val par = if (branchVal.isEmpty) parallelBranches(t) else None
          if (branchVal.nonEmpty) {
            // An effect defined here and run as a parallel branch.
            val outer = parallelAttrs
            parallelAttrs = deferred(t.asInstanceOf[ValDef].name)
            branchVal.foreach(traverseTree(_)(o))
            parallelAttrs = outer
          } else if (par.nonEmpty) {
            val (at, branches, rest) = par.get
            val outer = parallelAttrs
            for ((b, label) <- branches) {
              parallelAttrs = Map("parallel" -> at, "branch" -> label)
              traverseTree(b)(o)
            }
            parallelAttrs = outer
            rest.foreach(traverseTree(_)(o))
          } else {
            t match {
              case ref: Ref => addCall(from, ref)
              case _        =>
            }
            addSink(from, t, owner)
            addFrontend(from, t, owner)
            super.traverseTree(t)(o)
          }
        }
      }
    }
    traverser.traverseTree(tree)(owner)
  }

  private def addCall(from: String, ref: Ref): Unit = {
    val sym = ref.symbol
    if (sym.isNoSymbol || !own.contains(sym.maybeOwner)) return
    val at = pos(ref.pos, withEnd = false)
    if (sym.isDefDef && sym.flags.is(Flags.Deferred)) {
      val id = interfaceID(sym)
      if (!abstractCalls.contains(id)) {
        abstractCalls(id) = sym
        val owner = sym.owner
        graph.addNode(
          Node(id, "interface_call", s"${typeName(owner)}.${sym.name}", pkgName(sym), signature(sym), sym.pos.flatMap(pos(_, withEnd = false)))
        )
      }
      graph.addEdge(Edge(from, id, "calls", at, parallelAttrs))
    } else if ((sym.isDefDef && isMemberDef(sym)) || (sym.isValDef && isMemberVal(sym))) {
      // The target is a node once its class is walked; edges to nodes that
      // never appear are dropped on output.
      graph.addEdge(Edge(from, funcID(sym), "calls", at, parallelAttrs))
    }
  }

  // ZIO's combinators that run their operands alongside each other: binary
  // ones, whose chains (a <&> b <&> c) form one group, and those taking a
  // collection of effects or a function run once per element.
  private val zipParOps = Set("<&>", "<&", "&>", "zipPar", "zipParLeft", "zipParRight", "zipWithPar")
  private val collectParOps = Set("collectAllPar", "collectAllParDiscard", "mergeAllPar", "reduceAllPar", "validatePar")
  private val foreachParOps = Set("foreachPar", "foreachParDiscard", "validateParDiscard")

  /** The attributes of the local vals in tree whose effects run as a
    * parallel branch (`val b = ...; a <&> b`), by name: a for comprehension's
    * `b = ...` reaches the branch as a pattern variable of the same name, not
    * as the val.
    */
  private def deferredBranches(tree: Tree): Map[String, Map[String, String]] = {
    val found = mutable.HashMap.empty[String, Map[String, String]]
    new TreeTraverser {
      override def traverseTree(t: Tree)(o: Symbol): Unit = {
        for ((at, branches, _) <- parallelBranches(t); (b, label) <- branches) {
          b match {
            case id: Ident if !id.symbol.isNoSymbol && !own.contains(id.symbol.maybeOwner) =>
              found(id.name) = Map("parallel" -> at, "branch" -> label)
            case _ =>
          }
        }
        super.traverseTree(t)(o)
      }
    }.traverseTree(tree)(Symbol.noSymbol)
    found.toMap
  }

  /** The branches of a parallel combinator call t, labelled "1", "2", ... or
    * "each", its position as "file:line:col", and the rest of its trees
    * (implicit arguments), which run outside the branches.
    */
  private def parallelBranches(t: Tree): Option[(String, List[(Tree, String)], List[Tree])] = {
    def isZio(sym: Symbol) = !sym.isNoSymbol && sym.maybeOwner.fullName.startsWith("zio.")
    // A call's method, its qualifier and argument lists, innermost first.
    def parts(t: Tree): Option[(Symbol, Tree, List[List[Tree]])] = t match {
      case Apply(fun, args) => parts(fun).map((s, qual, argss) => (s, qual, argss :+ args))
      case TypeApply(fun, _) => parts(fun)
      case Select(qual, _)  => Some((t.symbol, qual, Nil))
      case Inlined(_, Nil, e) => parts(e)
      case _                => None
    }
    def unwrap(t: Tree): Tree = t match {
      case Inlined(_, Nil, e) => unwrap(e)
      case Typed(e, _)        => unwrap(e)
      case _                  => t
    }
    // The operands of a chain of binary parallel combinators.
    def operands(t: Tree): Option[(List[Tree], List[Tree])] = parts(unwrap(t)) match {
      case Some((sym, qual, (that :: Nil) :: implicits)) if zipParOps(sym.name) && isZio(sym) =>
        val (left, rest) = operands(qual).getOrElse((List(qual), Nil))
        Some((left :+ that, rest ++ implicits.flatten))
      case _ => None
    }
    def elements(arg: Tree): Option[List[Tree]] = unwrap(arg) match {
      case Repeated(elems, _)         => Some(elems)
      case Apply(_, List(r))          => elements(r)
      case _                          => None
    }
    lazy val at = pos(t.pos, withEnd = false).map(p => s"${p.file}:${p.startLine}:${p.startCol}")
    operands(t) match {
      case Some((ops, rest)) =>
        at.map(a => (a, ops.zipWithIndex.map((b, i) => (b, (i + 1).toString)), rest))
      case None =>
        parts(unwrap(t)) match {
          case Some((sym, qual, (first :: argss))) if isZio(sym) && collectParOps(sym.name) && first.size == 1 =>
            val branches = elements(first.head) match {
              case Some(es) if es.size > 1 => es.zipWithIndex.map((b, i) => (b, (i + 1).toString))
              case _                      => List((first.head, "each"))
            }
            at.map(a => (a, branches, qual :: argss.flatten))
          case Some((sym, qual, (first :: fs :: argss))) if isZio(sym) && foreachParOps(sym.name) && fs.size == 1 =>
            at.map(a => (a, List((fs.head, "each")), qual :: first ++ argss.flatten))
          case _ => None
        }
    }
  }

  /** Links each abstract method called to its implementations in the
    * concrete inspected classes that inherit it.
    */
  private def addDispatch(): Unit = {
    val concrete = own.toList.filter(c => !c.flags.is(Flags.Trait) && !c.flags.is(Flags.Abstract))
    for ((id, m) <- abstractCalls; c <- concrete) {
      val bases = c.typeRef.baseClasses
      if (bases.contains(m.owner)) {
        val impl = bases.iterator
          .map(b => m.overridingSymbol(b))
          .find(s => !s.isNoSymbol && !s.flags.is(Flags.Deferred))
        for (s <- impl if own.contains(s.owner) && !skipped(s)) {
          graph.addEdge(Edge(id, funcID(s), "dispatches_to"))
        }
      }
    }
  }

  // Naming.

  private[scala] def funcKind(sym: Symbol): String = if (sym.owner.flags.is(Flags.Module)) "func" else "method"

  private[scala] def funcID(sym: Symbol): String = {
    val owner = sym.owner
    val name = sym.name + overloadSuffix(sym)
    if (isPackageObject(owner)) s"func:${qualify(pkgName(owner), name)}"
    else if (owner.flags.is(Flags.Module)) s"func:${qualified(owner)}.$name"
    else s"method:(${qualified(owner)}).$name"
  }

  private[scala] def funcName(sym: Symbol): String = {
    val owner = sym.owner
    if (isPackageObject(owner)) sym.name else s"${typeName(owner)}.${sym.name}"
  }

  private def interfaceID(sym: Symbol): String = s"interface_call:(${qualified(sym.owner)}).${sym.name}${overloadSuffix(sym)}"

  private def typeID(cls: Symbol): String = s"type:${qualified(cls)}"

  // Overloaded methods are told apart by their parameter types:
  // "find(Long)", "find(String,Int)".
  private def overloadSuffix(sym: Symbol): String = {
    val overloads = sym.owner.declarations.count(d => d.name == sym.name && d.isTerm && !skipped(d))
    if (overloads <= 1) ""
    else {
      val params = sym.paramSymss.filterNot(_.exists(_.isTypeParam)).flatten
      params.map(p => show(info(p))).mkString("(", ",", ")")
    }
  }

  /** The package and the enclosing types: "com.example.Outer.Inner". */
  private[scala] def qualified(cls: Symbol): String = qualify(pkgName(cls), typeName(cls))

  private def qualify(pkg: String, name: String): String = if (pkg.isEmpty) name else s"$pkg.$name"

  /** The name within the package: "Outer.Inner". */
  private[scala] def typeName(cls: Symbol): String = {
    Iterator
      .iterate(cls)(_.maybeOwner)
      .takeWhile(s => !s.isNoSymbol && !s.isPackageDef)
      .filter(s => s.isClassDef && !isPackageObject(s))
      .map(_.name.stripSuffix("$"))
      .toList
      .reverse
      .mkString(".")
  }

  private[scala] def pkgName(sym: Symbol): String = {
    val pkg = Iterator.iterate(sym)(_.maybeOwner).find(s => s.isNoSymbol || s.isPackageDef).get
    if (pkg.isNoSymbol || pkg.fullName == "<empty>" || pkg.fullName == "<root>") "" else pkg.fullName
  }

  // The object holding a file's top-level definitions: "Validation$package".
  private def isPackageObject(sym: Symbol): Boolean = {
    sym.flags.is(Flags.Module) && sym.name.stripSuffix("$").endsWith("$package")
  }

  // Companion objects share their class's name, so only standalone objects
  // are types; package objects are not.
  private def hasTypeNode(cls: Symbol): Boolean = {
    own.contains(cls) && !isPackageObject(cls) && !cls.flags.is(Flags.Synthetic) &&
    !(cls.flags.is(Flags.Module) && !cls.companionClass.isNoSymbol)
  }

  /** The inspected types t mentions, type arguments included. */
  private def ownTypes(t: TypeRepr): List[Symbol] = {
    val out = mutable.LinkedHashSet.empty[Symbol]
    def loop(t: TypeRepr, depth: Int): Unit = if (depth < 20) {
      t.dealias match {
        case AppliedType(tycon, args) => (tycon :: args).foreach(loop(_, depth + 1))
        case AndType(a, b)            => loop(a, depth + 1); loop(b, depth + 1)
        case OrType(a, b)             => loop(a, depth + 1); loop(b, depth + 1)
        case MethodType(_, params, res) => (params :+ res).foreach(loop(_, depth + 1))
        case PolyType(_, _, res)      => loop(res, depth + 1)
        case ByNameType(u)            => loop(u, depth + 1)
        case AnnotatedType(u, _)      => loop(u, depth + 1)
        case Refinement(parent, _, _) => loop(parent, depth + 1)
        case TypeBounds(lo, hi)       => loop(lo, depth + 1); loop(hi, depth + 1)
        case other =>
          val s = other.typeSymbol
          if (s.isClassDef && hasTypeNode(s)) out += s
      }
    }
    loop(t, 0)
    out.toList
  }

  /** A def's signature as Scala writes it, "[T](id: Long): Task[T]", or a
    * val's type.
    */
  private[scala] def signature(sym: Symbol): String = {
    def loop(t: TypeRepr): String = t match {
      case PolyType(names, _, res) => names.mkString("[", ", ", "]") + loop(res)
      case mt @ MethodType(names, types, res) =>
        val using = if (mt.isImplicit) "using " else ""
        val params = names.zip(types).map((n, t) => s"$n: ${show(t)}").mkString(s"($using", ", ", ")")
        res match {
          case _: MethodType | _: PolyType => params + loop(res)
          case _                           => s"$params: ${show(res)}"
        }
      case ByNameType(u) => show(u)
      case other         => show(other)
    }
    try loop(info(sym))
    catch { case _: Exception => "" }
  }

  // Symbol.info is experimental; the widened reference is the same type.
  private[scala] def info(sym: Symbol): TypeRepr = sym.termRef.widen

  private[scala] def show(t: TypeRepr): String = t.show(using Printer.TypeReprShortCode)

  /** p relative to the project root, 1-based. */
  private[scala] def pos(p: Position, withEnd: Boolean): Option[Pos] = {
    try {
      val file = p.sourceFile.getJPath.map(_.toString).getOrElse(p.sourceFile.path)
      if (file.isEmpty) None
      else {
        val path = Paths.get(file)
        val rel = if (path.isAbsolute && path.normalize.startsWith(root)) root.relativize(path.normalize) else path
        val name = rel.toString.replace(java.io.File.separatorChar, '/')
        if (withEnd) Some(Pos(name, p.startLine + 1, p.startColumn + 1, p.endLine + 1, p.endColumn + 1))
        else Some(Pos(name, p.startLine + 1, p.startColumn + 1))
      }
    } catch { case _: Exception => None }
  }
}
