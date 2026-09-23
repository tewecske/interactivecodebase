package zioapp.backend.db

import io.getquill.NamingStrategy
import io.getquill.context.qzio.ZioJdbcContext
import io.getquill.context.sql.idiom.SqlIdiom
import zio.*

import javax.sql.DataSource

/** Discharges the DataSource that ctx.run needs, so repositories return a plain Task. */
abstract class QuillRepository[Dialect <: SqlIdiom, Naming <: NamingStrategy](
  dataSource: DataSource,
  protected val ctx: ZioJdbcContext[Dialect, Naming],
) {

  protected def run[T](query: ZIO[DataSource, Throwable, T]): Task[T] = {
    query.provideEnvironment(ZEnvironment(dataSource))
  }
}
