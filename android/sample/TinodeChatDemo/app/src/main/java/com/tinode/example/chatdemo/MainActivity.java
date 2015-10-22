/*****************************************************************************
 *
 * Copyright 2014, Tinode, All Rights Reserved
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 *  File        :  MainActivity.java
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description : 
 *
 *  Crete the main app activity
 *
 *****************************************************************************/
package com.tinode.example.chatdemo;

import android.app.ProgressDialog;
import android.os.AsyncTask;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.support.v4.app.Fragment;
import android.support.v4.app.FragmentManager;
import android.support.v4.app.FragmentPagerAdapter;
import android.support.v4.app.FragmentTransaction;
import android.support.v4.view.ViewPager;
import android.support.v7.app.ActionBar;
import android.support.v7.app.ActionBarActivity;
import android.text.format.Time;
import android.util.Log;
import android.view.LayoutInflater;
import android.view.Menu;
import android.view.MenuItem;
import android.view.View;
import android.view.ViewGroup;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.ListView;
import android.widget.TextView;
import android.widget.Toast;

import com.tinode.Tinode;
import com.tinode.rest.Request;
import com.tinode.rest.Rest;
import com.tinode.rest.model.Contact;
import com.tinode.streaming.model.Utils;
import com.tinode.streaming.Connection;
import com.tinode.streaming.MeTopic;
import com.tinode.streaming.NotConnectedException;
import com.tinode.streaming.PresTopic;
import com.tinode.streaming.Topic;

import java.net.MalformedURLException;
import java.net.URI;
import java.net.URL;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Vector;

public class MainActivity extends ActionBarActivity {
    private static final String TAG = "com.tinode.example.ChatDemo";
    // Generate your own API key
    private static final String APIKEY = "--GENERATE YOUR OWN KEY--";
    private static final String STATE_SELECTED_NAVIGATION_ITEM = "selected_navigation_item";

    private static final int FRAGMENT_IDX_LOGIN = 0;
    private static final int FRAGMENT_IDX_LOG = 1;
    private static final int FRAGMENT_IDX_CONTACTS = 2;

    private MainFragmentPagerAdapter mPageAdapter;
    private ActionBar mActionBar;
    private ViewPager mViewPager;
    private ActionBar.TabListener mTabListener;
    List<Fragment> mFragments;

	private Connection mChatConn;
	private PresTopic<String> mPresence;
	private MeTopic<String> mOnline;

	private ArrayAdapter<String> mConnectionLog;
    private ArrayAdapter<Contact> mContactList;

	@Override
	protected void onCreate(Bundle savedInstanceState) {
		super.onCreate(savedInstanceState);
		setContentView(R.layout.activity_main);

		mActionBar = getSupportActionBar();
		// Specify that tabs should be displayed in the action bar.
		mActionBar.setNavigationMode(ActionBar.NAVIGATION_MODE_TABS);
        mActionBar.setDisplayOptions(0, ActionBar.DISPLAY_SHOW_TITLE);

        mFragments = new Vector<Fragment>();
        mFragments.add(new LoginFragment());
        mFragments.add(new LogFragment());
        mFragments.add(new ContactsFragment());

        mPageAdapter = new MainFragmentPagerAdapter(getSupportFragmentManager(), mFragments);
        mViewPager = (ViewPager) findViewById(R.id.pager);
        mViewPager.setAdapter(mPageAdapter);
        mViewPager.setOnPageChangeListener(new ViewPager.SimpleOnPageChangeListener() {
            @Override
            public void onPageSelected(int position) {
                mActionBar.setSelectedNavigationItem(position);
            }
        });
		// Create a tab listener that is called when the user changes tabs.
		mTabListener = new ActionBar.TabListener() {
			@Override
			public void onTabSelected(ActionBar.Tab tab, FragmentTransaction ft) {
                mViewPager.setCurrentItem(tab.getPosition());
			}

            @Override
            public void onTabUnselected(ActionBar.Tab tab, FragmentTransaction ft) {

            }

            @Override
            public void onTabReselected(ActionBar.Tab tab, FragmentTransaction ft) {

            }
        };

		mActionBar.addTab(mActionBar.newTab().setText("Start").setTabListener(mTabListener));
		mActionBar.addTab(mActionBar.newTab().setText("Log").setTabListener(mTabListener));
        mActionBar.addTab(mActionBar.newTab().setText("Contacts").setTabListener(mTabListener));

		mConnectionLog = new ArrayAdapter<String>(this, android.R.layout.simple_list_item_1);
        mContactList = new ArrayAdapter<Contact>(this, android.R.layout.simple_list_item_1);
	}

	@Override
	public void onRestoreInstanceState(Bundle savedInstanceState) {
		// Restore the previously serialized current tab position.
		if (savedInstanceState.containsKey(STATE_SELECTED_NAVIGATION_ITEM)) {
			getActionBar().setSelectedNavigationItem(savedInstanceState
                    .getInt(STATE_SELECTED_NAVIGATION_ITEM));
		}
	}

	@Override
	public void onSaveInstanceState(Bundle outState) {
		// Serialize the current tab position.
		outState.putInt(STATE_SELECTED_NAVIGATION_ITEM, getActionBar()
				.getSelectedNavigationIndex());
	}

	@Override
	public boolean onCreateOptionsMenu(Menu menu) {
		// Inflate the menu; this adds items to the action bar if it is present.
		getMenuInflater().inflate(R.menu.main, menu);
		return true;
	}

	@Override
	public boolean onOptionsItemSelected(MenuItem item) {
		// Handle action bar item clicks here. The action bar will
		// automatically handle clicks on the Home/Up button, so long
		// as you specify a parent activity in AndroidManifest.xml.
		int id = item.getItemId();
		if (id == R.id.action_start_chat) {
            Toast.makeText(this, "This button does nothing", Toast.LENGTH_SHORT).show();
			return true;
		}
		return super.onOptionsItemSelected(item);
	}

	public void onConnectButton(View connect) {
		Log.d(TAG, "Connect clicked");
		String hostName = ((EditText) findViewById(R.id.edit_hostname))
				.getText().toString();
		Log.d(TAG, "Connect to host: " + hostName);

        // TODO(gene): the initialization placed here just for debugging. In production initialization
        // should happen in onCreate. This code should just call getInstance().
        try {
            Tinode.initialize(new URL(hostName), APIKEY);
        } catch (MalformedURLException e) {
            Toast.makeText(this, "Invalid URL", Toast.LENGTH_SHORT).show();
        }
        mChatConn = Connection.getInstance();

        // Set a handler on the main thread to be used for calling EventListener methods.
        mChatConn.setHandler(new Handler(Looper.getMainLooper()));
        mChatConn.setListener(new Connection.EventListener() {
			@Override
			public void onConnect(int code, String reason, Map<String, Object> params) {
				Log.d(TAG, "Connected");
				postToLog(getString(R.string.connected) + ", vers: " + Utils.getStringParam(params, "protocol"));
                LoginFragment lf = (LoginFragment) mFragments.get(FRAGMENT_IDX_LOGIN);
                lf.setConnectionStatus(true);
			}

			@Override
			public void onLogin(int code, String text) {
				Log.d(TAG, "Login done: " + text + "(" + code +")");
				postToLog(text + " (" + code + ")");
                LoginFragment lf = (LoginFragment) mFragments.get(FRAGMENT_IDX_LOGIN);
                lf.setLoginStatus(code == 200);
			}

            @Override
            public Topic<?> onNewTopic(String topicName) {
                postToLog("new topic " + topicName);

                if (!topicName.startsWith(Connection.TOPIC_P2P)) {
                    Log.i(TAG, "Don't know how to create topic " + topicName);
                    return null;
                }

                return startNewChat(topicName);
            }

            @Override
			public void onDisconnect(int code, String reason) {
				Log.d(TAG, "Disconnected");
				postToLog(getString(R.string.disconnected) + " " + reason + " (" + code + ")");
                LoginFragment lf = (LoginFragment) mFragments.get(FRAGMENT_IDX_LOGIN);
                lf.setConnectionStatus(false);
                lf.setLoginStatus(false);
			}
		});

		mChatConn.Connect(false);
		Toast.makeText(this, "Connecting...", Toast.LENGTH_SHORT).show();
	}

	public void onLoginButton(View login) {
		Log.d(TAG, "Login clicked");
		if (mChatConn == null) {
			Toast.makeText(this, "Not connected", Toast.LENGTH_SHORT).show();
			return;
		}

		String username = ((EditText) findViewById(R.id.edit_username))
				.getText().toString();
		String password = ((EditText) findViewById(R.id.edit_password))
				.getText().toString();
		try {
			mChatConn.Login(username, password);
		} catch (NotConnectedException e) {
			Toast.makeText(this, "Not connected", Toast.LENGTH_SHORT).show();
			Log.d(TAG, e.toString());
		}
	}

    public void onPublishButton(View send) {
        int tab = mActionBar.getSelectedNavigationIndex();
        ChatFragment chatUi = (ChatFragment) mFragments.get(tab);
        String text = chatUi.getMessage();
        Log.d(TAG, "onPublish(" + chatUi.getTopicName() + ", " + text + ")");

        try {
            chatUi.getTopic().Publish(text);
        } catch (NotConnectedException e) {
            Toast.makeText(this, "Not connected", Toast.LENGTH_SHORT).show();
            Log.d(TAG, e.toString());
        }
    }

    public void onStartTopicButton(View go) {
        if (mChatConn != null && mChatConn.isAuthenticated()) {
            int tab = mActionBar.getSelectedNavigationIndex();
            ContactsFragment contacts = (ContactsFragment) mFragments.get(tab);
            String name = contacts.getTopicName().trim();
            if (name.length() > 0) {
                startNewChat(name);
            }
        } else {
            Toast.makeText(this, "Must login first", Toast.LENGTH_SHORT);
        }
    }

	private void setCheckBox(int id, boolean state) {
	    CheckBox cb = ((CheckBox) findViewById(id));
		if (cb != null) {
			cb.setChecked(state);
		}
	}

    private Topic<?> startNewChat(final String topicName) {
        final ChatFragment chatUi = new ChatFragment();
        Topic<String> topic = null;

        if (topicName.startsWith(Connection.TOPIC_P2P)) {
            topic = mOnline.startP2P(topicName, new MeTopic.MeListener<String>() {
                ChatFragment mChatUi = chatUi;

                @Override
                public void onSubscribe(int code, String text) {

                }

                @Override
                public void onUnsubscribe(int code, String text) {
                    mChatUi.logMessage("SYS", "unsubscribed");
                    postToLog(topicName + " unsubscribed");
                }

                @Override
                public void onPublish(String party, int code, String text) {
                    mChatUi.clearSendField();
                    postToLog(topicName + " posted (" + code + ") " + text);
                }

                @Override
                public void onData(boolean isReply, String content) {
                    String from = isReply ? topicName : "me";
                    mChatUi.logMessage(from, content);
                    postToLog(from.substring(0, 5) + "...:" + content);
                }
            });
        } else {
            topic = new Topic<String>(mChatConn, topicName, String.class, new Topic.Listener<String>() {
                ChatFragment mChatUi = chatUi;

                @Override
                public void onSubscribe(int code, String text) {
                    // This won't be called, no need to implement
                }

                @Override
                public void onUnsubscribe(int code, String text) {
                    mChatUi.logMessage("SYS", "unsubscribed");
                    postToLog(topicName + " unsubscribed");
                }

                @Override
                public void onPublish(String topicName, int code, String text) {
                    mChatUi.clearSendField();
                    postToLog(topicName + " posted (" + code + ") " + text);
                }

                @Override
                public void onData(String from, String content) {
                    mChatUi.logMessage(from, content);
                    postToLog(from.substring(0, 5) + "...:" + content);
                }
            });
        }
        chatUi.setTopic(topic);
        mFragments.add(chatUi);
        mPageAdapter.notifyDataSetChanged();
        mActionBar.addTab(mActionBar.newTab().setText("Chat").setTabListener(mTabListener), true);

        Log.d(TAG, "New topic created [" + topicName + "]");

        return topic;
    }

	public void onSubscribeMeCheckbox(View subsme) {
		Log.d(TAG, "[Subscribe !me] clicked");
		if (mChatConn == null) {
			Toast.makeText(this, "Not connected", Toast.LENGTH_SHORT).show();
			((CheckBox) subsme).setChecked(false);
			return;
		}
		if (mOnline == null) {
			mOnline = new MeTopic<String>(mChatConn, String.class, new MeTopic.MeListener<String>() {
				@Override
				public void onSubscribe(final int code, final String text) {
					Log.d(TAG, "me.onSubscribe (" + code +") " + text);
					setCheckBox(R.id.subscribe_me, mOnline.isSubscribed());
                    postToLog("!me subscribed");
				}

				@Override
				public void onUnsubscribe(final int code, final String text) {
					Log.d(TAG, "me.onUnsubscribe (" + code +") " + text);
					setCheckBox(R.id.subscribe_me, mOnline.isSubscribed());
                    postToLog("!me unsubscribed");
				}

				@Override
				public void onPublish(String party, int code, String text) {
					Log.d(TAG, "me.onPublish (" + code + ") " + text);
                    postToLog("" + code + " " + text);
				}

				@Override
				public void onData(boolean isReply, final String content) {
					Log.d(TAG, "me.onData(" + isReply + ", '" + content + "') " + content);
					postToLog("!me: " + content);
					Toast.makeText(MainActivity.this, "Message received: " + content,
							Toast.LENGTH_SHORT).show();
				}
			});
		}
		try {
			Toast.makeText(this, "Requesting !me...", Toast.LENGTH_SHORT).show();

			if (!((CheckBox) subsme).isChecked()) {
				mOnline.Unsubscribe();
			} else {
				mOnline.Subscribe();
			}
		} catch (NotConnectedException e) {
			Log.d(TAG, e.toString());
		}
		((CheckBox) subsme).setChecked(false);
	}

	public void onSubscribePresCheckbox(View subspres) {
		Log.d(TAG, "[Subscribe !pres] clicked");
		if (mChatConn == null) {
			Toast.makeText(this, "Not connected", Toast.LENGTH_SHORT).show();
			((CheckBox) subspres).setChecked(false);
			return;
		}

		if (mPresence == null) {
			mPresence = new PresTopic<String>(mChatConn, String.class,
                    new PresTopic.PresListener<String>() {
				@Override
				public void onSubscribe(int code, String text) {
					Log.d(TAG, "pres.onSubscribe (" + code +") " + text);
					setCheckBox(R.id.subscribe_pres, mPresence.isSubscribed());
                    postToLog("!pres subscribed");
				}

				@Override
				public void onUnsubscribe(int code, String text) {
					Log.d(TAG, "pres.onSubscribe (" + code +") " + text);
					setCheckBox(R.id.subscribe_pres, mPresence.isSubscribed());
                    postToLog("!pres unsubscribed");
				}

				@Override
				public void onData(String who, Boolean online, String status) {
                    String strOnline = (online ? "ONL" : "OFFL");
					Log.d(TAG, who + " is " + strOnline
                            + " with status: " + (status == null ? "null" : "'" + status + "'"));
                    postToLog(who.substring(0, 5) + "... is " + strOnline + "(" + status + ")");
				}
			});
		}

		try {
			Toast.makeText(this, "Requesting !pres...", Toast.LENGTH_SHORT).show();

			if (!((CheckBox) subspres).isChecked()) {
				mPresence.Unsubscribe();
			} else {
				mPresence.Subscribe();
			}
		} catch (NotConnectedException e) {
			Log.d(TAG, e.toString());
		}

		((CheckBox) subspres).setChecked(false);
	}

    public void postToLog(String msg) {
        Time now = new Time();
        now.setToNow();
        mConnectionLog.insert(now.format("%H:%M:%S") + " " + msg, 0);
    }

    public class MainFragmentPagerAdapter extends FragmentPagerAdapter {
        private List<Fragment> mFragments;

        public MainFragmentPagerAdapter(FragmentManager fm, List<Fragment> fragments) {
            super(fm);
            mFragments = fragments;
        }

        @Override
        public int getCount() {
            return mFragments.size();
        }

        @Override
        public Fragment getItem(int position) {
            return mFragments.get(position);
        }
    }

	/**
	 * Chat UI.
	 */
	public class ChatFragment extends Fragment {
        private Topic<String> mTopic;
        private View mChatView;

		public ChatFragment() {
        }

        public void setTopic(Topic<String> topic) {
            mTopic = topic;
        }
        public Topic<String> getTopic() {
            return mTopic;
        }

        public String getTopicName() {
            return mTopic.getName();
        }

        public String getMessage() {
            return ((EditText) mChatView.findViewById(R.id.editChatMessage)).getText().toString();
        }

        public void clearSendField() {
            ((EditText) mChatView.findViewById(R.id.editChatMessage)).setText("");
        }

        public void logMessage(String who, String text) {
            TextView log = (TextView) findViewById(R.id.chatLog);
            CharSequence msg;
            if (who.equals(mChatConn.getMyUID())) {
                msg = "me: ";
            } else if (who.length() > 3) {
                int len = who.length();
                msg = who.subSequence(len-2, len-1) + ": ";
            } else {
                msg = who + ": ";
            }
            log.append(msg + text + "\n");
        }

		@Override
		public View onCreateView(LayoutInflater inflater, ViewGroup container,
				Bundle savedInstanceState) {
			mChatView = inflater.inflate(R.layout.fragment_chat, container, false);
            ((TextView) mChatView.findViewById(R.id.topic_name)).setText(mTopic.getName());
			setHasOptionsMenu(true);
			return mChatView;
		}
	}

	/**
	 * Login fragment.
	 */
	public class LoginFragment extends Fragment {
        private View mLoginView;

		public LoginFragment() {
		}

		@Override
		public View onCreateView(LayoutInflater inflater, ViewGroup container,
								 Bundle savedInstanceState) {
			mLoginView = inflater.inflate(R.layout.fragment_login, container, false);
			if (mOnline != null && mOnline.isSubscribed()) {
				((CheckBox) mLoginView.findViewById(R.id.subscribe_me)).setChecked(true);
			}
			if (mPresence != null && mPresence.isSubscribed()) {
				((CheckBox) mLoginView.findViewById(R.id.subscribe_me)).setChecked(true);
			}
            setConnectionStatus(mChatConn != null && mChatConn.isConnected());
            setLoginStatus(mChatConn != null && mChatConn.isAuthenticated());
			setHasOptionsMenu(true);
			return mLoginView;
		}

        public void setConnectionStatus(boolean status) {
            if (status) {
                ((TextView) mLoginView.findViewById(R.id.connection_status)).setText(R.string.connected);
            } else {
                ((TextView) mLoginView.findViewById(R.id.connection_status)).setText(R.string.disconnected);
            }
        }

        public void setLoginStatus(boolean status) {
            if (status) {
                ((TextView) mLoginView.findViewById(R.id.login_status)).setText(R.string.logged_in);
            } else {
                ((TextView) mLoginView.findViewById(R.id.login_status)).setText(R.string.not_authenticated);
            }
        }
	}

	/**
	 * Contacts/start conversation fragment.
	 */
	public class ContactsFragment extends Fragment {
        private View mContactView;

		public ContactsFragment() {
		}

		@Override
		public View onCreateView(LayoutInflater inflater, ViewGroup container,
								 Bundle savedInstanceState) {
			mContactView = inflater.inflate(R.layout.fragment_contacts, container, false);
            final ListView contacts = ((ListView) mContactView.findViewById(R.id.contact_list));
            contacts.setAdapter(mContactList);
            contacts.setOnItemClickListener(new AdapterView.OnItemClickListener() {
                @Override
                public void onItemClick(AdapterView<?> adapterView, View view, int pos, long id) {
                    Contact contact = (Contact) contacts.getItemAtPosition(pos);
                    ((EditText) mContactView.findViewById(R.id.editTopicName)).setText(
                            MeTopic.topicNameForContact(contact.contactId));
                }
            });

            if (mContactList.isEmpty() && mChatConn != null && mChatConn.isAuthenticated()) {
                new AsyncTask<Void, Void, ArrayList<Contact>>() {
                    ProgressDialog dialog;

                    @Override
                    protected void onPreExecute() {
                        dialog = ProgressDialog.show(MainActivity.this, null,
                                "Fetching contacts...");
                    }

                    @Override
                    protected ArrayList<Contact> doInBackground(Void... ignored) {
                        Request req = Rest.buildRequest()
                                .setMethod("GET")
                                .setInDataType(Contact.class)
                                .addKind("contacts");
                        return Rest.executeBlocking(req);
                    }

                    @Override
                    protected void onPostExecute(ArrayList<Contact> result) {
                        if (result != null) {
                            mContactList.clear();
                            mContactList.addAll(result);
                            mContactList.notifyDataSetChanged();
                        }
                        dialog.dismiss();
                    }
                }.execute();
            }

            if (mChatConn == null || !mChatConn.isAuthenticated()) {
                Toast.makeText(MainActivity.this, "Must login first", Toast.LENGTH_SHORT).show();
            }

			setHasOptionsMenu(true);
			return mContactView;
		}

        public String getTopicName() {
            return ((EditText) mContactView.findViewById(R.id.editTopicName)).getText().toString();
        }
	}

	/**
	 * Log fragment.
	 */
	public class LogFragment extends Fragment {

		public LogFragment() {
		}

		@Override
		public View onCreateView(LayoutInflater inflater, ViewGroup container,
								 Bundle savedInstanceState) {
			View rootView = inflater.inflate(R.layout.fragment_log, container, false);
			((ListView) rootView.findViewById(R.id.connection_log)).setAdapter(mConnectionLog);
			setHasOptionsMenu(true);
			return rootView;
		}
	}
}
