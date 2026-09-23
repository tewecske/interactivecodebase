// icb-scala: reads the TASTy of a compiled Scala 3 project and writes icb's
// code graph (docs/graph.schema.json). `sbt launcher` builds it and writes
// target/icb-scala, the script icb runs.

val scala3Version = "3.8.4"

lazy val launcher = taskKey[File]("Writes target/icb-scala, which runs the extractor with its classpath")

lazy val root = project
  .in(file("."))
  .settings(
    name := "icb-scala",
    scalaVersion := scala3Version,
    scalacOptions ++= Seq("-deprecation", "-feature", "-unchecked", "-Werror"),
    libraryDependencies ++= Seq(
      "org.scala-lang" %% "scala3-tasty-inspector" % scala3Version,
      "org.scalameta" %% "munit" % "1.3.6" % Test,
    ),
    // The tests inspect the TASTy of src/test/scala/sample and need a real
    // java.class.path; the machine is shared, so the heap is capped.
    Test / fork := true,
    Test / javaOptions ++= Seq("-Xmx1g", "-Xss8m"),
    launcher := {
      val cp = (Compile / fullClasspath).value.files.map(_.getAbsolutePath).mkString(java.io.File.pathSeparator)
      val out = target.value / "icb-scala"
      IO.write(
        out,
        s"""#!/bin/sh
           |# Written by `sbt launcher` in extractors/scala. ICB_SCALA_JAVA_OPTS
           |# replaces the default heap cap.
           |exec java $${ICB_SCALA_JAVA_OPTS:--Xmx1500m -Xss8m} -cp '$cp' icb.scala.Main "$$@"
           |""".stripMargin,
      )
      out.setExecutable(true)
      out
    },
  )
