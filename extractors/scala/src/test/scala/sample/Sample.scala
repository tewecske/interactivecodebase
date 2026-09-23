package sample

// Code the extractor tests read the TASTy of.

final case class Item(id: Int, name: String)

trait Store {
  def save(item: Item): Unit

  def load(id: Int): Option[Item]
}

final class MemStore extends Store {
  private var items = Map.empty[Int, Item]

  def save(item: Item): Unit = items = items.updated(item.id, item)

  def load(id: Int): Option[Item] = items.get(id)
}

abstract class Logging {
  def log(msg: String): Unit = println(msg)
}

object NullStore extends Logging with Store {
  def save(item: Item): Unit = log(s"dropped ${item.id}")

  def load(id: Int): Option[Item] = None
}

class Service(store: Store) {
  def put(items: List[Item]): Unit = items.foreach(i => store.save(i))

  def get(id: Int): Option[Item] = store.load(id).orElse(fallback(id))

  def find(id: Int): Option[Item] = get(id)

  def find(name: String): Option[Item] = None

  private def fallback(id: Int): Option[Item] = {
    def local(n: Int) = Item(n, "fallback")
    Some(local(id))
  }
}

object Service {
  def default: Service = new Service(new MemStore)
}

def run(): Unit = Service.default.put(List(Item(1, "one")))
