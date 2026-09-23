package sample.data

// Database and other sinks the extractor tests read the TASTy of.

import io.getquill.*
import io.getquill.jdbczio.Quill
import zio.*
import zio.http.{Client, Request, URL}

import java.nio.file.{Files, Paths}
import java.sql.Connection
import javax.sql.DataSource

final case class Account(id: Long, displayName: String, email: String, active: Boolean)
final case class Login(id: Long, accountId: Long, at: Long)
final case class AuditEntry(id: Long, message: String)

final class Accounts(ds: DataSource) {
  val ctx = new PostgresZioJdbcContext(SnakeCase)
  import ctx.*

  private inline def accounts = quote(querySchema[Account]("accounts", _.displayName -> "name"))
  private inline def logins = quote(querySchema[Login]("logins"))

  def byEmail(email: String): Task[Option[Account]] = {
    val q = quote(accounts.filter(a => a.email == lift(email)))
    ctx.run(q).map(_.headOption).provideEnvironment(ZEnvironment(ds))
  }

  def names: Task[List[String]] = ctx.run(accounts.filter(_.active).map(_.displayName)).provideEnvironment(ZEnvironment(ds))

  def withLogins(id: Long): Task[List[(Account, Login)]] = {
    val q = quote {
      for {
        a <- accounts.filter(_.id == lift(id))
        l <- logins.join(l => l.accountId == a.id)
      } yield (a, l)
    }
    ctx.run(q).provideEnvironment(ZEnvironment(ds))
  }

  def create(a: Account): Task[Long] =
    ctx.run(accounts.insertValue(lift(a)).returningGenerated(_.id)).provideEnvironment(ZEnvironment(ds))

  def rename(id: Long, name: String): Task[Long] =
    ctx.run(accounts.filter(_.id == lift(id)).update(_.displayName -> lift(name))).provideEnvironment(ZEnvironment(ds))

  def remove(id: Long): Task[Long] = ctx.run(logins.filter(_.accountId == lift(id)).delete).provideEnvironment(ZEnvironment(ds))

  def audit(entries: List[AuditEntry]): Task[List[Long]] =
    ctx.run(liftQuery(entries).foreach(e => query[AuditEntry].insertValue(e))).provideEnvironment(ZEnvironment(ds))
}

object Raw {
  val table = "accounts"

  def count(c: Connection): Int = {
    val rs = c.prepareStatement("SELECT count(*) FROM " + table + " WHERE active").executeQuery()
    rs.next()
    rs.getInt(1)
  }

  def skunkFind: skunk.Fragment[Long] = {
    import skunk.implicits.*
    sql"SELECT id, email FROM accounts WHERE id = ${skunk.codec.all.int8}"
  }
}

object Outside {
  def home: String = sys.env.getOrElse("HOME", "/")

  def port: Option[String] = Option(java.lang.System.getenv("PORT"))

  def read(name: String): String = Files.readString(Paths.get(s"/etc/$name"))

  def fetch(client: Client): Task[String] =
    ZIO.scoped(client.batched(Request.get(URL.decode("https://example.com/api").toOption.get)).flatMap(_.body.asString))

  def mail(m: jakarta.mail.Message): Unit = jakarta.mail.Transport.send(m)
}
