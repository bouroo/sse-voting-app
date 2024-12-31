<script>
  import axios from "axios";
  import { onMount } from "svelte";

  /**
   * @type {Array<{ name: string; votes: number }>}
   */
  let candidates = [];
  let voting = false; // Indicates if a vote is in progress
  let voted = false; // Indicates if the user has voted
  let errorMessage = ""; // For displaying error messages
  let loading = true; // Indicates loading state for fetching results

  async function fetchResults() {
    loading = true; // Set loading to true while fetching results
    try {
      const response = await axios.get("http://localhost:8080/results");
      candidates = response.data.sort(
        (
          /** @type {{ name: string; }} */ a,
          /** @type {{ name: string; }} */ b
        ) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())
      );
    } catch (error) {
      errorMessage = "Error fetching results. Please try again later.";
      console.error("Error fetching results:", error);
    } finally {
      loading = false; // Set loading to false after fetching
    }
  }

  /**
   * @param {string} candidate
   */
  async function vote(candidate) {
    voting = true; // Set voting to true while voting
    errorMessage = ""; // Reset error message
    try {
      await axios.get(`http://localhost:8080/vote?candidate=${candidate}`);
      voted = true; // Set voted to true after voting
      // Optionally, you can set a timeout to reset the voted state
      setTimeout(() => {
        voted = false; // Allow voting again after 5 seconds
      }, 5000);
    } catch (error) {
      errorMessage = "Error voting. Please try again.";
      console.error("Error voting:", error);
    } finally {
      voting = false; // Set voting to false after voting
    }
  }

  function setupSSE() {
    const eventSource = new EventSource("http://localhost:8080/events");

    eventSource.onmessage = function (event) {
      const updatedCandidate = JSON.parse(event.data);
      const index = candidates.findIndex(
        (c) => c.name === updatedCandidate.name
      );
      if (index !== -1) {
        candidates[index].votes = updatedCandidate.votes;
      }
    };

    eventSource.onerror = function (err) {
      console.error("EventSource failed:", err);
      eventSource.close();
      // Optionally, you could try to reconnect after some time.
    };
  }

  onMount(() => {
    fetchResults(); // Fetch initial results
    setupSSE(); // Set up SSE listener
  });
</script>

<main>
  <h1>ระบบลงคะแนนเลือกตั้ง</h1>
  {#if loading}
    <p>กำลังโหลดข้อมูลผู้สมัคร...</p>
  {:else if errorMessage}
    <p class="error">{errorMessage}</p>
  {:else if candidates.length > 0}
    {#each candidates as candidate}
      <div class="candidate" role="button" aria-pressed={voted} tabindex="0">
        <span>{candidate.name}</span>
        <span>Votes: {candidate.votes}</span>
        <button
          on:click={() => vote(candidate.name)}
          disabled={voted || voting}
        >
          Vote
        </button>
      </div>
    {/each}
  {:else}
    <p>ไม่มีผู้สมัครให้แสดง</p>
  {/if}
</main>

<style>
  h1 {
    text-align: center;
    margin: 20px;
  }
  .candidate {
    display: flex;
    justify-content: space-between;
    margin: 10px;
    padding: 10px;
    border: 1px solid #ccc;
    border-radius: 5px;
  }
  button {
    padding: 10px;
    border: none;
    border-radius: 5px;
    cursor: pointer;
    background-color: #28a745;
    color: #fff;
  }
  button:disabled {
    background-color: #ccc;
    cursor: not-allowed;
  }
  .error {
    color: red;
    text-align: center;
  }
</style>
