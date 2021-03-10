package tinode

import java.util.Base64
import java.util.concurrent.ConcurrentHashMap

import scala.collection.JavaConverters._
import scala.collection._
import scala.concurrent.duration._

import io.gatling.core.Predef._
import io.gatling.http.Predef._

class Loadtest extends TinodeBase {
  // Input file can be set with the "accounts" java option.
  // E.g. JAVA_OPTS="-Daccounts=/tmp/z.csv" gatling.sh -sf . -rsf . -rd "na" -s tinode.Loadtest
  val feeder = csv(System.getProperty("accounts", "users.csv")).random

  val scn = scenario("WebSocket")
    .exec(ws("Connect WS").connect("/v0/channels?apikey=AQEAAAABAAD_rAp4DJh05a1HAwFT3A6K"))
    .exec(session => session.set("id", "tn-" + session.userId))
    .pause(1)
    .exec(hello)
    .pause(1)
    .feed(feeder)
    .doIfOrElse({session =>
      val uname = session("username").as[String]
      var token = session("token").asOption[String]
      if (token == None) {
        token = tokenCache.get(uname)
      }
      token == None
    }) { loginBasic } { loginToken }
    .exitHereIfFailed
    .exec(subMe)
    .exitHereIfFailed
    .exec(getSubs)
    .exitHereIfFailed
    .doIf({session =>
      session.attributes.contains("subs")
    }) {
      exec { session =>
        // Shuffle subscriptions.
        val subs = session("subs").as[Vector[String]]
        val shuffled = scala.util.Random.shuffle(subs.toList)
        session.set("subs", shuffled)
      }
      .foreach("${subs}", "sub") {
        exec(subTopic)
        .exitHereIfFailed
        .pause(0, 2)
        .doIfOrElse({session =>
          val topic = session("sub").as[String]
          !topic.startsWith("chn")
        }) { publish } { pause(5) }
        .exec(leaveTopic)
        .pause(0, 3)
      }
    }
    .exec(ws("close-ws").close)

  setUp(scn.inject(rampUsers(numSessions) during (rampPeriod.seconds))).protocols(httpProtocol)
}

class MeLoadtest extends TinodeBase {
  val username = System.getProperty("username", "user0")
  val password = System.getProperty("password", "user0123")

  val scn = scenario("WebSocket")
    .exec(ws("Connect WS").connect("/v0/channels?apikey=AQEAAAABAAD_rAp4DJh05a1HAwFT3A6K"))
    .exec(session => session.set("id", "tn-" + session.userId))
    .exec(session => session.set("username", username))
    .exec(session => session.set("password", password))
    .pause(1)
    .exec(hello)
    .pause(1)
    .doIfOrElse({session =>
      val uname = session("username").as[String]
      val token = tokenCache.get(username)
      token == None
    }) { loginBasic } { loginToken }
    .exitHereIfFailed
    .exec(subMe)
    .exitHereIfFailed
    .exec(getSubs)
    .exitHereIfFailed
    .pause(1000)
    .exec(ws("close-ws").close)

  setUp(scn.inject(rampUsers(numSessions) during (rampPeriod.seconds))).protocols(httpProtocol)
}

class SingleTopicLoadtest extends TinodeBase {
  // Input file can be set with the "accounts" java option.
  // E.g. JAVA_OPTS="-Daccounts=/tmp/z.csv" gatling.sh -sf . -rsf . -rd "na" -s tinode.Loadtest
  val feeder = csv(System.getProperty("accounts", "users.csv")).random
  val topic = System.getProperty("topic", "TOPIC_NAME")

  val scn = scenario("WebSocket")
    .exec(ws("Connect WS").connect("/v0/channels?apikey=AQEAAAABAAD_rAp4DJh05a1HAwFT3A6K"))
    .exec(session => session.set("id", "tn-" + session.userId))
    .pause(1)
    .exec(hello)
    .pause(1)
    .feed(feeder)
    .doIfOrElse({session =>
      val uname = session("username").as[String]
      var token = session("token").asOption[String]
      if (token == None) {
        token = tokenCache.get(uname)
      }
      token == None
    }) { loginBasic } { loginToken }
    .exitHereIfFailed
    .exec(subMe)
    .exitHereIfFailed
    .exec(getSubs)
    .exitHereIfFailed
    .doIf({session =>
      session.attributes.contains("subs")
    }) {
      exec(session => session.set("sub", topic))
      .exec(subTopic)
      .exitHereIfFailed
      .pause(0, 10)
      .exec(publish)
      .pause(15)
      .exec(leaveTopic)
      .pause(0, 3)
    }
    .exec(ws("close-ws").close)

  setUp(scn.inject(rampUsers(numSessions) during (rampPeriod.seconds))).protocols(httpProtocol)
}
