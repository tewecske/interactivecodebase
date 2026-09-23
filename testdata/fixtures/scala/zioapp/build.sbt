// A small zio-http + Quill app shaped like gathedge: shared domain types,
// routes calling a service trait, its implementation and a Quill
// repository. icb's Scala tests extract its code graph.

val scala3Version = "3.8.4"
val zioHttpVersion = "3.11.3"
val zioJsonVersion = "0.9.1"
val quillVersion = "4.8.6"

ThisBuild / scalaVersion := scala3Version
ThisBuild / organization := "example.zioapp"
ThisBuild / evictionErrorLevel := Level.Warn

lazy val shared = project
  .in(file("modules/shared"))
  .settings(
    name := "shared",
    libraryDependencies ++= Seq(
      "dev.zio" %% "zio-json" % zioJsonVersion,
      "dev.zio" %% "zio-http" % zioHttpVersion,
    ),
  )

lazy val backend = project
  .in(file("modules/backend"))
  .dependsOn(shared)
  .settings(
    name := "backend",
    libraryDependencies ++= Seq(
      "dev.zio" %% "zio-http" % zioHttpVersion,
      "io.getquill" %% "quill-jdbc-zio" % quillVersion,
      "org.postgresql" % "postgresql" % "42.7.13",
    ),
  )

lazy val root = project
  .in(file("."))
  .aggregate(shared, backend)
