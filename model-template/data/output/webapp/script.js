window.addEventListener("message", (event) => {
  if (event.data.type === "data") {
    renderTable(event.data.text);
  }
});

function renderTable(data) {
  try {
    const populationTable = document.getElementById("populationTable");
    const parsedData = JSON.parse(atob(data));
    console.log("parsedData: ", parsedData);
    // Check if data is empty or invalid
    if (!parsedData || !Array.isArray(parsedData) || parsedData.length === 0) {
      populationTable.innerHTML = `<p>No population data available</p>`;
      return;
    }

    let tableHTML = `
      <table>
        <thead>
          <tr>
            <th>Rank</th>
            <th>Country</th>
            <th>Population</th>
          </tr>
        </thead>
        <tbody>
    `;

    parsedData.forEach((item, index) => {
      tableHTML += `
        <tr>
          <td>${index + 1}</td>
          <td>${item.country}</td>
          <td>${item.population.toLocaleString()}</td>
        </tr>
      `;
    });

    tableHTML += `</tbody></table>`;
    document.getElementById("populationTable").innerHTML = tableHTML;
  } catch (error) {
    populationTable.innerHTML = `<p>Invalid or corrupted data</p>`;
  }
}
