package zioapp.backend.db

import io.getquill.jdbczio.Quill
import zio.*

import javax.sql.DataSource

object DataSourceFactory {
  val live: ZLayer[Any, Throwable, DataSource] = Quill.DataSource.fromPrefix("db")
}
