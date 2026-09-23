package icb.scala

import java.nio.charset.StandardCharsets
import java.nio.file.{Files, Path, Paths}
import scala.jdk.CollectionConverters.*
import scala.tasty.inspector.TastyInspector

/** The classes of one part of a project and the classpath they compile
  * against. The classes are the project's own code; the classpath holds its
  * libraries and is only read to resolve types.
  */
final case class Module(classpath: List[String], classDirs: List[String])

final case class Args(root: Path, out: Option[Path], modules: List[Module])

/** icb-scala --root DIR [-o FILE] (--classpath CP CLASSDIR...)...
  *
  * Reads the .tasty files under each CLASSDIR, resolving what they refer to
  * against CP, and writes the code graph as JSON to FILE (default stdout).
  * Each --classpath starts a module: an sbt project's own class directories
  * with its classpath. Positions are relative to DIR.
  */
object Main {

  val usage: String = "usage: icb-scala --root DIR [-o FILE] (--classpath CP CLASSDIR...)..."

  def main(argv: Array[String]): Unit = {
    parse(argv.toList) match {
      case Left(err) =>
        System.err.println(s"icb-scala: $err\n$usage")
        sys.exit(2)
      case Right(args) =>
        try {
          val json = run(args).toJson
          args.out match {
            case Some(p) => Files.writeString(p, json, StandardCharsets.UTF_8)
            case None    => print(json)
          }
        } catch {
          case e: Exception =>
            System.err.println(s"icb-scala: $e")
            sys.exit(1)
        }
    }
  }

  def parse(argv: List[String]): Either[String, Args] = {
    def loop(rest: List[String], acc: Args): Either[String, Args] = rest match {
      case Nil                          => Right(acc.copy(modules = acc.modules.reverse.map(u => u.copy(classDirs = u.classDirs.reverse))))
      case "--root" :: dir :: tail      => loop(tail, acc.copy(root = Paths.get(dir)))
      case ("-o" | "--out") :: f :: tail => loop(tail, acc.copy(out = Some(Paths.get(f))))
      case "--classpath" :: cp :: tail =>
        val entries = cp.split(java.io.File.pathSeparator).toList.filter(_.nonEmpty)
        loop(tail, acc.copy(modules = Module(entries, Nil) :: acc.modules))
      case flag :: _ if flag.startsWith("-") => Left(s"unknown or incomplete flag $flag")
      case dir :: tail =>
        acc.modules match {
          case u :: us => loop(tail, acc.copy(modules = u.copy(classDirs = dir :: u.classDirs) :: us))
          case Nil     => loop(tail, acc.copy(modules = List(Module(Nil, List(dir)))))
        }
    }
    loop(argv, Args(Paths.get("."), None, Nil)).flatMap { args =>
      if (args.modules.forall(_.classDirs.isEmpty)) Left("no class directories") else Right(args)
    }
  }

  /** Inspects every module into one graph. */
  def run(args: Args): Graph = {
    val graph = new Graph
    val root = args.root.toAbsolutePath.normalize
    for (u <- args.modules if u.classDirs.nonEmpty) {
      val tasty = u.classDirs.flatMap(tastyFiles)
      if (tasty.nonEmpty) {
        val ok = TastyInspector.inspectAllTastyFiles(tasty, Nil, u.classDirs ++ u.classpath)(new Extractor(root, graph))
        if (!ok) throw new RuntimeException(s"reading the TASTy in ${u.classDirs.mkString(", ")} failed")
      }
    }
    graph
  }

  /** The .tasty files under dir, sorted. */
  def tastyFiles(dir: String): List[String] = {
    val p = Paths.get(dir)
    if (!Files.isDirectory(p)) Nil
    else {
      val s = Files.walk(p)
      try s.iterator.asScala.map(_.toString).filter(_.endsWith(".tasty")).toList.sorted
      finally s.close()
    }
  }
}
