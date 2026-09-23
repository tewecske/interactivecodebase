// A small zio-http + Quill + Laminar app shaped like gathedge: shared domain
// types and API paths (cross-built for the JVM and Scala.js), routes calling
// a service trait, its implementation and a Quill repository, and a Laminar
// frontend with Waypoint pages calling the API. icb's Scala tests extract
// its code graph.

import sbtcrossproject.CrossPlugin.autoImport.{crossProject, CrossType}

val scala3Version = "3.8.4"
val zioHttpVersion = "3.11.3"
val zioJsonVersion = "0.9.1"
val quillVersion = "4.8.6"
val laminarVersion = "17.2.1"
val waypointVersion = "10.0.0-M7"

ThisBuild / scalaVersion := scala3Version
ThisBuild / organization := "example.zioapp"
ThisBuild / evictionErrorLevel := Level.Warn

// Pure: one source tree in modules/shared/src for both platforms.
lazy val shared = crossProject(JSPlatform, JVMPlatform)
  .crossType(CrossType.Pure)
  .in(file("modules/shared"))
  .settings(
    name := "shared",
    libraryDependencies ++= Seq(
      "dev.zio" %%% "zio-json" % zioJsonVersion,
      "dev.zio" %%% "zio-http" % zioHttpVersion,
    ),
  )

lazy val sharedJVM = shared.jvm
lazy val sharedJS = shared.js

lazy val backend = project
  .in(file("modules/backend"))
  .dependsOn(sharedJVM)
  .settings(
    name := "backend",
    libraryDependencies ++= Seq(
      "dev.zio" %% "zio-http" % zioHttpVersion,
      "io.getquill" %% "quill-jdbc-zio" % quillVersion,
      "org.postgresql" % "postgresql" % "42.7.13",
    ),
  )

lazy val frontend = project
  .in(file("modules/frontend"))
  .enablePlugins(ScalaJSPlugin)
  .dependsOn(sharedJS)
  .settings(
    name := "frontend",
    libraryDependencies ++= Seq(
      "com.raquo" %%% "laminar" % laminarVersion,
      "com.raquo" %%% "waypoint" % waypointVersion,
    ),
  )

lazy val root = project
  .in(file("."))
  .aggregate(sharedJVM, sharedJS, backend, frontend)
