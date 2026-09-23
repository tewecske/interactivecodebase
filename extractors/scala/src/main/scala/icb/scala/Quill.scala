package icb.scala

import scala.collection.mutable
import scala.quoted.*

/** Reads the query of a Quill (ProtoQuill) ctx.run call and writes it as
  * SQL that icb can parse to link tables and columns.
  *
  * TASTy holds the query as the quoted Scala: Quill's macros turn it into SQL
  * only after the TASTy is written. The query is followed through the vals
  * and inline defs it uses (`inline def users = quote(querySchema[User]("users"))`):
  *
  *   - `querySchema[T]("t", _.field -> "col")` and `query[T]`, and their
  *     dynamic forms, are tables, the latter named after T by the
  *     context's naming strategy;
  *   - insertValue, insert, updateValue, update and delete make the table
  *     they apply to the statement's target, and the fields they set its
  *     columns (every field for insertValue and updateValue, less those
  *     returningGenerated returns);
  *   - fields of a table's row type read anywhere in the query (filter,
  *     map, join, sortBy) are its columns, and so are all the fields of a
  *     row the query returns.
  *
  * The SQL keeps only what icb links: `SELECT t0.a, t1.b FROM x t0, y t1`,
  * `INSERT INTO x (a, b) VALUES ($1, $2)`, `UPDATE x t0 SET a = $1 WHERE
  * t0.id = $2`, `DELETE FROM x t0 WHERE ...`, with other tables in an
  * EXISTS. A query with no table found is "{?}" and partial.
  */
private[scala] trait Quill { self: Walk =>
  import q.reflect.*

  private[scala] final case class QuillSQL(sql: String, partial: Boolean)

  private final case class Table(name: String, row: Symbol, renames: Map[String, String])

  private val quillActions = Set("insertValue", "insert", "updateValue", "update", "delete")

  private val reserved = Set(
    "all", "analyse", "analyze", "and", "any", "array", "as", "asc", "both", "case", "cast", "check", "collate", "column",
    "constraint", "create", "default", "desc", "distinct", "do", "else", "end", "except", "false", "fetch", "for", "foreign",
    "from", "grant", "group", "having", "in", "into", "intersect", "is", "join", "lateral", "leading", "limit", "not", "null",
    "offset", "on", "only", "or", "order", "placing", "primary", "references", "returning", "select", "some", "table", "then",
    "to", "trailing", "true", "union", "unique", "user", "using", "values", "when", "where", "window", "with",
  )

  private[scala] def quillQuery(query: Term, ctx: Option[Term], owner: Symbol): QuillSQL = {
    val naming = namingOf(ctx)
    val scan = new QuillScan(owner, naming)
    scan.visit(query)
    // The rows the query returns: every column of their tables is read.
    if (scan.action.isEmpty) {
      val rows = mutable.LinkedHashSet.empty[Symbol]
      def loop(t: TypeRepr, depth: Int): Unit = if (depth < 10) {
        t.dealias match {
          case AppliedType(tycon, args) => args.foreach(loop(_, depth + 1))
          case other                    => rows += other.typeSymbol
        }
      }
      loop(query.tpe.widen, 0)
      for (row <- rows; field <- fields(row)) scan.reads += ((row, field))
    }
    render(scan, naming)
  }

  private final class QuillScan(owner: Symbol, naming: String => String) {
    val tables = mutable.ListBuffer.empty[Table]
    val reads = mutable.ListBuffer.empty[(Symbol, String)]
    var action = Option.empty[String]
    var target = Option.empty[Table]
    // The fields the action sets; None for all of them.
    var sets: Option[List[String]] = Some(Nil)
    val returning = mutable.ListBuffer.empty[String]
    val generated = mutable.ListBuffer.empty[String]
    private val expanded = mutable.HashSet.empty[Symbol]

    def visit(t: Tree): Unit = traverser.traverseTree(t)(owner)

    private val traverser: TreeTraverser = new TreeTraverser {
      override def traverseTree(t: Tree)(o: Symbol): Unit = if (!handled(t)) super.traverseTree(t)(o)
    }

    private def handled(t: Tree): Boolean = t match {
      case TypeApply(fn, List(row)) if (fn.symbol.name == "query" || fn.symbol.name == "dynamicQuery") && isQuill(fn.symbol) =>
        addTable(Table(naming(row.tpe.typeSymbol.name), row.tpe.typeSymbol, Map.empty))
        true
      case a: Apply =>
        val (fn, args) = call(a)
        val sym = fn.symbol
        if (sym.isNoSymbol || !isQuill(sym)) false
        else if (sym.name == "querySchema" || sym.name == "dynamicQuerySchema") {
          querySchema(a, args)
          true
        } else if (sym.name.startsWith("lift") || sym.name.startsWith("lazyLift")) {
          // The Scala value lifted in, not columns.
          true
        } else if (quillActions(sym.name) && action.isEmpty) {
          actionOf(fn, sym.name, args)
          true
        } else if (sym.name.startsWith("returning")) {
          fn match {
            case Select(recv, _) => visit(recv)
            case _               =>
          }
          val fs = args.flatMap(fieldsIn)
          returning ++= fs
          if (sym.name == "returningGenerated") generated ++= fs
          true
        } else false
      case Select(recv, "delete") if isQuill(t.symbol) && action.isEmpty =>
        actionOf(t.asInstanceOf[Term], "delete", Nil)
        true
      case Select(qual, name) if isField(qual, name) =>
        reads += ((rowOf(qual), name))
        visit(qual)
        true
      case r: Ref if isExpandable(r.symbol) =>
        expanded += r.symbol
        (members.get(r.symbol) orElse localsOf(owner).get(r.symbol)).foreach(visit)
        r match {
          case Select(qual, _) => visit(qual)
          case _               =>
        }
        true
      case _ => false
    }

    private def isExpandable(sym: Symbol): Boolean = {
      !sym.isNoSymbol && !expanded(sym) && !sym.flags.is(Flags.Module) &&
      ((own.contains(sym.maybeOwner) && members.contains(sym)) || localsOf(owner).contains(sym))
    }

    private def addTable(tbl: Table): Unit = if (!tables.exists(t => t.name == tbl.name && t.row == tbl.row)) tables += tbl

    private def querySchema(t: Term, args: List[Term]): Unit = {
      val row = typeRows(t.tpe.widen).headOption.getOrElse(Symbol.noSymbol)
      val name = args.headOption.map(stringValue(_, owner, 0)).getOrElse(Hole)
      val renames = args.drop(1).flatMap(lambda).flatMap(_.rhs).flatMap(assignment).collect {
        case (field, Literal(StringConstant(col))) => field -> col
      }
      if (name != Hole && !row.isNoSymbol) addTable(Table(name, row, renames.toMap))
    }

    /** The action applied to a query: `q.update(...)`, or an extension of
      * the context applied to it, `ctx.insertValue(q)(...)`.
      */
    private def actionOf(fn: Term, name: String, args: List[Term]): Unit = {
      action = Some(name.stripSuffix("Value"))
      val (recv, rest) = fn match {
        case Select(qual, _) if isQuery(qual) => (Some(qual), args)
        case _                                => (args.headOption, args.drop(1))
      }
      val before = tables.size
      recv.foreach(visit)
      target = tables.lift(before).orElse(recv.flatMap(r => typeRows(r.tpe.widen).flatMap(row => tables.find(_.row == row)).headOption))
      if (name.endsWith("Value")) sets = None
      else {
        val assigned = rest.flatMap(lambda).flatMap(_.rhs).flatMap(assignment)
        sets = Some(assigned.map(_._1))
        assigned.foreach((_, value) => visit(value))
      }
    }

    /** `_.field -> value`, as ArrowAssoc(_.field).->(value). */
    private def assignment(t: Term): Option[(String, Term)] = strip(t) match {
      case a: Apply =>
        call(a) match {
          case (Select(left, "->"), List(value)) =>
            val field = strip(left) match {
              case b: Apply => call(b)._2.headOption.flatMap(fieldsIn(_).headOption)
              case other    => fieldsIn(other).headOption
            }
            field.map(_ -> value)
          case _ => None
        }
      case _ => None
    }

    /** The row fields t selects: `_.id`, `(_.a, _.b)`. */
    private def fieldsIn(t: Tree): List[String] = {
      val out = mutable.ListBuffer.empty[String]
      val tr = new TreeTraverser {
        override def traverseTree(x: Tree)(o: Symbol): Unit = x match {
          case Select(qual, name) if isField(qual, name) => out += name
          case _                                         => super.traverseTree(x)(o)
        }
      }
      tr.traverseTree(t)(owner)
      out.toList
    }
  }

  private def isQuill(sym: Symbol): Boolean = {
    !sym.isNoSymbol && !sym.maybeOwner.isNoSymbol && sym.maybeOwner.fullName.startsWith("io.getquill")
  }

  private def isQuery(t: Term): Boolean = isType(t, "io.getquill.Query") || isType(t, "io.getquill.Quoted")

  private def rowOf(t: Term): Symbol = t.tpe.widen.dealias.typeSymbol

  /** Reports whether qual.name reads a case class field. */
  private def isField(qual: Term, name: String): Boolean = {
    try {
      val row = rowOf(qual)
      row.isClassDef && row.flags.is(Flags.Case) && row.caseFields.exists(_.name == name)
    } catch { case _: Exception => false }
  }

  private def fields(row: Symbol): List[String] = {
    if (row.isClassDef && row.flags.is(Flags.Case)) row.caseFields.map(_.name) else Nil
  }

  private def typeRows(t: TypeRepr): List[Symbol] = t.dealias match {
    case AppliedType(_, args) => args.flatMap(typeRows)
    case other                => List(other.typeSymbol)
  }

  /** The naming strategy in the context's type: SnakeCase unless it says
    * Literal, LowerCase or UpperCase.
    */
  private def namingOf(ctx: Option[Term]): String => String = {
    val names = ctx.map(c => show(c.tpe.widen)).getOrElse("")
    if (names.contains("Literal")) identity
    else if (names.contains("LowerCase")) _.toLowerCase
    else if (names.contains("UpperCase")) _.toUpperCase
    else snakeCase
  }

  private[scala] def snakeCase(s: String): String = {
    s.flatMap(c => if (c.isUpper) s"_${c.toLower}" else c.toString).stripPrefix("_")
  }

  private def render(scan: QuillScan, naming: String => String): QuillSQL = {
    val tables = scan.tables.toList
    if (tables.isEmpty) return QuillSQL(Hole, partial = true)
    def ident(s: String) = if (s.matches("[a-z_][a-z0-9_]*") && !reserved(s)) s else "\"" + s.replace("\"", "\"\"") + "\""
    def column(t: Table, field: String) = ident(t.renames.getOrElse(field, naming(field)))
    val alias = tables.zipWithIndex.map((t, i) => t -> s"t$i").toMap
    // Each read field goes to the first table of its row type.
    def reads(t: Table): List[String] = {
      if (tables.find(_.row == t.row).contains(t)) scan.reads.toList.collect { case (row, f) if row == t.row => f }.distinct.map(column(t, _))
      else Nil
    }
    def from(ts: List[Table]) = ts.map(t => s"${ident(t.name)} ${alias(t)}").mkString(", ")
    def selectList(ts: List[Table]) = {
      val cols = ts.flatMap(t => reads(t).map(c => s"${alias(t)}.$c"))
      if (cols.isEmpty) "*" else cols.mkString(", ")
    }
    var param = 0
    def next(): String = { param += 1; "$" + param }
    val returning = if (scan.returning.isEmpty) "" else scan.target.fold("") { t =>
      " RETURNING " + scan.returning.distinct.map(column(t, _)).mkString(", ")
    }

    (scan.action, scan.target) match {
      case (None, _) => QuillSQL(s"SELECT ${selectList(tables)} FROM ${from(tables)}", partial = false)
      case (Some(_), None) => QuillSQL(s"SELECT ${selectList(tables)} FROM ${from(tables)}", partial = true)
      case (Some(op), Some(t)) =>
        val others = tables.filterNot(_ == t)
        val exists = if (others.isEmpty) Nil else List(s"EXISTS (SELECT ${selectList(others)} FROM ${from(others)})")
        val sets = scan.sets.getOrElse(fields(t.row).filterNot(scan.generated.contains)).distinct.map(column(t, _))
        def where = {
          val conds = reads(t).map(c => s"${alias(t)}.$c = ${next()}") ++ exists
          if (conds.isEmpty) "" else " WHERE " + conds.mkString(" AND ")
        }
        val sql = op match {
          case "insert" =>
            val cols = if (sets.isEmpty) "" else sets.mkString(" (", ", ", ")")
            val values = if (others.isEmpty) s"VALUES (${sets.map(_ => next()).mkString(", ")})" else s"SELECT ${selectList(others)} FROM ${from(others)}"
            s"INSERT INTO ${ident(t.name)}$cols $values$returning"
          case "update" =>
            val assigned = if (sets.isEmpty) fields(t.row).map(column(t, _)) else sets
            s"UPDATE ${ident(t.name)} ${alias(t)} SET ${assigned.map(c => s"$c = ${next()}").mkString(", ")}$where$returning"
          case _ => s"DELETE FROM ${ident(t.name)} ${alias(t)}$where$returning"
        }
        QuillSQL(sql, partial = false)
    }
  }
}
