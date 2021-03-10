package tinode

import java.util.Base64
import java.util.concurrent.ConcurrentHashMap

import scala.collection.JavaConverters._
import scala.collection._
import scala.concurrent.duration._

import io.gatling.core.Predef._
import io.gatling.http.Predef._

class TinodeBase extends Simulation {
  val httpProtocol = http
    .baseUrl("http://localhost:6060")
    .wsBaseUrl("ws://localhost:6060")

  // Auth tokens to share between sessions.
  val tokenCache : concurrent.Map[String, String] = new ConcurrentHashMap() asScala
  // Total number of messages to publish to a topic.
  val publishCount = Integer.getInteger("publish_count", 10).toInt
  // Maximum interval between publishing messages to a topic.
  val publishInterval = Integer.getInteger("publish_interval", 100).toInt
  // Total number of sessions.
  val numSessions = Integer.getInteger("num_sessions", 10000)
  // Ramp up period (0 to numSessions) in seconds.
  val rampPeriod = java.lang.Long.getLong("ramp", 300L)

  val hello = exitBlockOnFail {
    exec {
      ws("hi").sendText(
        """{"hi":{"id":"afabb3","ver":"0.16","ua":"Gatling-Loadtest/1.0; gatling/1.7.0"}}"""
      )
      .await(15 seconds)(
        ws.checkTextMessage("hi")
          .matching(jsonPath("$.ctrl").find.exists)
      )
    }
  }

  val loginBasic = exitBlockOnFail {
    exec { session =>
      val uname = session("username").as[String]
      val password = session("password").as[String]
      val secret = new String(java.util.Base64.getEncoder.encode((uname + ":" + password).getBytes()))
      session.set("secret", secret)
    }
    .exec {
      ws("login").sendText(
        """{"login":{"id":"${id}-login","scheme":"basic","secret":"${secret}"}}"""
      )
      .await(15 seconds)(
        ws.checkTextMessage("login-ctrl")
          .matching(jsonPath("$.ctrl").find.exists)
          .check(jsonPath("$.ctrl.params.token").saveAs("token"))
      )
    }
    .exec { session =>
      val uname = session("username").as[String]
      val token = session("token").as[String]
      tokenCache.put(uname, token)
      session
    }
  }

  val loginToken = exitBlockOnFail {
    exec { session =>
      val uname = session("username").as[String]
      var token = session("token").asOption[String]
      if (token == None) {
        token = tokenCache.get(uname)
      }
      session.set("token", token.getOrElse(""))
    }
    .exec {
      ws("login-token").sendText(
        """{"login":{"id":"${id}-login2","scheme":"token","secret":"${token}"}}"""
      )
      .await(15 seconds)(
        ws.checkTextMessage("login-ctrl")
          .matching(jsonPath("$.ctrl").find.exists)
      )
    }
  }

  val subMe = exitBlockOnFail {
    exec {
      ws("sub-me").sendText(
        """{"sub":{"id":"${id}-sub-me","topic":"me","get":{"what":"desc"}}}"""
      )
      .await(15 seconds)(
        ws.checkTextMessage("sub-me-desc")
          .matching(jsonPath("$.ctrl").find.exists)
          .check(jsonPath("$.ctrl.code").ofType[Int].in(200 to 299))
      )
    }
  }

  val subTopic = exitBlockOnFail {
    exec {
      ws("sub-topic").sendText(
        """{"sub":{"id":"${id}-sub-${sub}","topic":"${sub}","get":{"what":"desc sub data del"}}}"""
      )
      .await(15 seconds)(
        ws.checkTextMessage("sub-topic-ctrl")
          .matching(jsonPath("$.ctrl").find.exists)
          .check(jsonPath("$.ctrl.code").ofType[Int].in(200 to 299))
      )
    }
  }

  val publish = exitBlockOnFail {
    exec {
      repeat(publishCount, "i") {
        exec {
          ws("pub-topic").sendText(
            """{"pub":{"id":"${id}-pub-${sub}-${i}","topic":"${sub}","content":"This is a Tsung test ${i}"}}"""
          )
          .await(15 seconds)(
            ws.checkTextMessage("pub-topic-ctrl")
              .matching(jsonPath("$.ctrl").find.exists)
              .check(jsonPath("$.ctrl.code").ofType[Int].in(200 to 299))
          )
        }
        .pause(0, publishInterval)
      }
    }
  }

  val getSubs = exitBlockOnFail {
    exec {
      ws("get-subs").sendText(
        """{"get":{"id":"${id}-get-subs","topic":"me","what":"sub"}}"""
      )
      .await(15 seconds)(
        ws.checkTextMessage("save-subs")
          .matching(jsonPath("$.meta.sub").find.exists)
          .check(jsonPath("$.meta.sub[*].topic").findAll.saveAs("subs"))
      )
    }
  }

  val leaveTopic = exitBlockOnFail {
    exec {
      ws("leave-topic").sendText(
        """{"leave":{"id":"${id}-leave-${sub}","topic":"${sub}"}}"""
      )
      .await(15 seconds)(
        ws.checkTextMessage("sub-topic-ctrl")
          .matching(jsonPath("$.ctrl").find.exists)
      )
    }
  }
}
